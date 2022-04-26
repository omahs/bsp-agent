package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	gcp "cloud.google.com/go/storage"
	"github.com/covalenthq/bsp-agent/internal/config"
	"github.com/covalenthq/bsp-agent/internal/utils"
	pinner "github.com/covalenthq/ipfs-pinner"
	pincore "github.com/covalenthq/ipfs-pinner/core"
	"github.com/ipfs/go-cid"
	log "github.com/sirupsen/logrus"
)

type StorageManager struct {
	StorageConfig *config.StorageConfig

	GcpStore   *gcp.Client
	LocalStore *LocalStoreClient
	IpfsStore  *pinner.PinnerNode
}

func NewStorageManager(conf *config.StorageConfig) (*StorageManager, error) {
	manager := &StorageManager{}
	manager.StorageConfig = conf

	manager.setupGcpStore()
	manager.setupIpfsPinner()
	manager.setupLocalFs()

	if manager.GcpStore == nil && manager.IpfsStore == nil {
		return nil, fmt.Errorf("cannot setup gcp store or ipfs store")
	}

	return manager, nil
}

func (manager *StorageManager) GenerateLocation(ctx context.Context, segmentName string, replicaSegmentAvro []byte) (string, cid.Cid) {
	config := manager.StorageConfig
	var replicaURL string
	var ccid cid.Cid = cid.Undef
	var err error
	switch {
	case manager.GcpStore != nil:
		replicaURL = "https://storage.cloud.google.com/" + config.ReplicaBucketLoc + "/" + segmentName
	case manager.IpfsStore != nil:
		ccid, err = generateCidFor(ctx, *manager.IpfsStore, replicaSegmentAvro)
		if err != nil {
			log.Errorf("error generating cid for %s. Error: %s", config.BinaryFilePath, err)
			replicaURL = "only local: " + config.BinaryFilePath
		} else {
			replicaURL = "ipfs://" + ccid.String()
		}

	default:
		replicaURL = "only local: " + config.BinaryFilePath
	}

	return replicaURL, ccid
}

func (manager *StorageManager) Store(ctx context.Context, ccid cid.Cid, filename string, data []byte) error {
	// write to local store
	var err error

	if manager.StorageConfig.BinaryFilePath != "" {
		err = validatePath(manager.StorageConfig.BinaryFilePath, filename)
		if err != nil {
			return err
		}
		err = manager.writeToLocalStore(manager.StorageConfig.BinaryFilePath, filename, data)
		if err != nil {
			return err
		}
	}

	// ipfs store has priority over gcp
	if manager.IpfsStore != nil {
		if ccid == cid.Undef {
			return fmt.Errorf("cid is Undefined")
		}
		var ucid cid.Cid
		ucid, err = manager.handleObjectUploadToIPFS(ctx, ccid, filename)
		log.Info("client side cid is: %s, while uploaded is: %s", ccid, ucid)
	} else if manager.GcpStore != nil {
		err = manager.writeToCloudStorage(ctx, filename, data)
	}

	return err
}

func (manager *StorageManager) Close() {
	if manager.GcpStore != nil {
		err := manager.GcpStore.Close()
		if err != nil {
			log.Error("error in closing storage client: ", err)
		}
	}
}

func (manager *StorageManager) setupGcpStore() {
	// setup gcp storage
	storageConfig := manager.StorageConfig
	gcpStorageClient, err := utils.NewGCPStorageClient(storageConfig.GcpSvcAccountAuthFile)
	if err != nil {
		log.Printf("unable to get gcp storage client; --gcp-svc-account flag not set or set incorrectly: %v, storing BSP files locally: %v", err, storageConfig.GcpSvcAccountAuthFile)
		return
	}

	manager.GcpStore = gcpStorageClient
}

func (manager *StorageManager) setupIpfsPinner() {
	pinnode, err := getPinnerNode(pincore.PinningService(manager.StorageConfig.IpfsServiceType), manager.StorageConfig.IpfsServiceToken)
	if err != nil {
		log.Fatalf("error creating pinner node: %v", err)
		return
	}

	manager.IpfsStore = &pinnode
}

func (manager *StorageManager) setupLocalFs() {
	if manager.StorageConfig.BinaryFilePath == "" {
		log.Warn("--binary-file-path flag not provided to write block-replica avro encoded binary files to local path")
	}
}

func (manager *StorageManager) handleObjectUploadToIPFS(ctx context.Context, ccid cid.Cid, binaryFileName string) (cid.Cid, error) {
	// assuming that bin files are written (rather than cloud only storage)
	// need to explore strategy to directly upload in memory byte array via pinner
	var file *os.File
	var err error
	pinnode := *manager.IpfsStore
	if pinnode.PinService().ServiceType() == pincore.Web3Storage {
		file, err = generateCarFile(ctx, *manager.IpfsStore, ccid)
	} else {
		objPath := objectFilePath(binaryFileName, manager.StorageConfig.BinaryFilePath)
		file, err = os.Open(filepath.Clean(objPath))
	}

	if err != nil {
		return cid.Undef, fmt.Errorf("failure in opening/generating file for upload: %v", err)
	}

	fcid, err := pinnode.PinService().UploadFile(ctx, file)
	if err != nil {
		return cid.Undef, fmt.Errorf("failure in uploading specimen object to IPFS: %v", err)
	}

	log.Infof("File %s successfully uploaded to IPFS with pin: %s", file.Name(), fcid.String())

	return fcid, nil
}

//nolint:gosec
func (manager *StorageManager) writeToLocalStore(path, objectName string, object []byte) error {
	var _, err = os.Stat(filepath.Join(path, filepath.Base(objectName)))
	if os.IsNotExist(err) {
		fileSave, err := os.Create(filepath.Join(path, filepath.Base(objectName)))
		if err != nil {
			return fmt.Errorf("error in writing binary file: %w", err)
		}
		defer func() {
			if err := fileSave.Close(); err != nil {
				log.Error("Error closing file: ", err)
			}
		}()
		_, err = fileSave.Write(object)
		if err != nil {
			panic(err)
		}
	} else {
		log.Info("File already exists at: ", path, objectName)
	}
	log.Info("File written successfully to: ", path, objectName)

	return nil
}

func (manager *StorageManager) writeToCloudStorage(ctx context.Context, filename string, object []byte) error {
	ctx, cancel := context.WithTimeout(ctx, time.Second*time.Duration(uploadTimeout))
	defer cancel()

	bucket := manager.StorageConfig.ReplicaBucketLoc
	wc := manager.GcpStore.Bucket(bucket).Object(filename).NewWriter(ctx)
	if _, err := io.Copy(wc, bytes.NewReader(object)); err != nil {
		return fmt.Errorf("error in copying data to file: %w", err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("error in closing file: %w", err)
	}
	log.Info("File successfully uploaded to: https://storage.cloud.google.com/" + bucket + "/" + filename)

	return nil
}

func validatePath(path, objectName string) error {
	fileInfo, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("path does not exist: %w", err)
	}
	mode := fileInfo.Mode()
	if mode.IsDir() {
		log.Info("Writing block-replica binary file to local directory: ", path, objectName)
	}

	return nil
}

func objectFilePath(filename, binaryLocalPath string) string {
	return filepath.Join(binaryLocalPath, filepath.Base(filename))
}
