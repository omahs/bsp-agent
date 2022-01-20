package websocket

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"time"

	"cloud.google.com/go/storage"
	"github.com/covalenthq/mq-store-agent/internal/config"
	"github.com/covalenthq/mq-store-agent/internal/proof"
	st "github.com/covalenthq/mq-store-agent/internal/storage"
	"github.com/covalenthq/mq-store-agent/internal/types"
	"github.com/covalenthq/mq-store-agent/internal/utils"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/gorilla/websocket"
	"github.com/linkedin/goavro/v2"
	log "github.com/sirupsen/logrus"
)

func ConsumeWebsocketsEvents(config *config.EthConfig, websocketURL string, replicaCodec *goavro.Codec, ethClient *ethclient.Client, storageClient *storage.Client, binaryLocalPath, replicaBucket, proofChain string) {
	ctx := context.Background()
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)

	urlReceiveData := url.URL{Scheme: "ws", Host: websocketURL, Path: "/block"}
	log.Printf("connecting to %s", urlReceiveData.String())
	connectionReceiveData, _, err := websocket.DefaultDialer.Dial(urlReceiveData.String(), nil)
	if err != nil {
		log.Fatal("dial:", err)
	}
	defer func() {
		if cerr := connectionReceiveData.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	urlAcknowledgeData := url.URL{Scheme: "ws", Host: websocketURL, Path: "/acknowledge"}
	log.Printf("connecting to %s", urlAcknowledgeData.String())
	connectionAcknowledgeData, _, err := websocket.DefaultDialer.Dial(urlAcknowledgeData.String(), nil)
	if err != nil {
		log.Fatal("dial:", err)
	}
	defer func() {
		if cerr := connectionAcknowledgeData.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	done := make(chan struct{})

	go func() {
		defer close(done)
		for {
			_, message, err := connectionReceiveData.ReadMessage()
			if err != nil {
				log.Error("error in websocket message:", err)

			}
			res := &types.ElrondBlockResult{}
			errDecode := utils.DecodeAvro(res, message)
			if errDecode != nil {
				log.Error("could not decode block", errDecode)
			}
			log.Info("Received block hash: %v, nonce: %v", hex.EncodeToString(res.Block.Hash), res.Block.Nonce)
			log.Info("Sending back acknowledged hash...")

			errAcknowledgeData := connectionAcknowledgeData.WriteMessage(websocket.BinaryMessage, res.Block.Hash)
			if errAcknowledgeData != nil {
				log.Error("could not send acknowledged hash :(", errAcknowledgeData)
			}

			segmentName := fmt.Sprint(res.Block.ShardID) + "-" + fmt.Sprint(res.Block.Nonce) + "-" + "segment"
			proofTxHash := make(chan string, 1)
			go proof.SendBlockReplicaProofTx(ctx, config, proofChain, ethClient, uint64(res.Block.Nonce), 1, message, proofTxHash)
			pTxHash := <-proofTxHash
			if pTxHash != "" {
				log.Info("Proof-chain tx hash: ", pTxHash, " for block-replica segment: ", segmentName)
				err := st.HandleObjectUploadToBucket(ctx, storageClient, binaryLocalPath, replicaBucket, segmentName, pTxHash, message)
				if err != nil {
					log.Error("error in handling object upload and storage", err)
				}
			} else {
				log.Error("failed to prove & upload block-replica segment from websocket event: %v", segmentName)
			}
		}
	}()

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
		case <-interrupt:
			log.Println("interrupt")
			// Cleanly close the connection by sending a close message and then
			// waiting (with timeout) for the server to close the connection.
			err := connectionReceiveData.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			_ = connectionAcknowledgeData.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			if err != nil {
				log.Error("write close:", err)
				return
			}
			select {
			case <-done:
			case <-time.After(time.Second):
			}
			return
		}
	}
}
