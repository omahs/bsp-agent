// Package utils contains all the utilites used across the repo
package utils

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	"cloud.google.com/go/storage"
	"github.com/covalenthq/mq-store-agent/internal/config"
	"github.com/elodina/go-avro"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/go-redis/redis/v7"
	log "github.com/sirupsen/logrus"
	"google.golang.org/api/option"
)

// provides a new redis client using a redis config
func NewRedisClient(redisConnection string, redisConfig *config.RedisConfig) (*redis.Client, string, string, error) {
	var pwd string
	redisURL, err := url.Parse(redisConnection)
	if err != nil {
		log.Fatalf("unable to parse redis connection string: %v", err)
	}

	pass, _ := redisURL.User.Password()
	if pass != "" {
		log.Fatal("Please remove password from connection string cli flag and add it in .envrc as `REDIS_PWD`")
	} else {
		pwd = redisConfig.Password
	}

	dbString := strings.ReplaceAll(redisURL.Path, "/", "")
	m, err := url.ParseQuery(redisURL.RawQuery)
	if err != nil {
		log.Fatalf("unable to parse redis connection string query string: %v", err)
	}

	dbInt, err := strconv.Atoi(dbString)
	if err != nil {
		panic(err)
	}

	redisClient := redis.NewClient(&redis.Options{
		Addr:     redisURL.Host,
		Password: pwd,
		DB:       dbInt, // use default DB
	})
	streamKey := m["topic"][0]
	consumerGroup := redisURL.Fragment
	_, err = redisClient.Ping().Result()

	return redisClient, streamKey, consumerGroup, err
}

// initializes a new ethereum client using an address string
func NewEthClient(address string) (*ethclient.Client, error) {
	ethClient, err := ethclient.Dial(address)
	if err != nil {
		log.Error("error in getting eth client: ", err.Error())
	}

	return ethClient, nil
}

// initializes a new storage client using a service account string
func NewStorageClient(serviceAccount string) (*storage.Client, error) {
	ctx := context.Background()
	storageClient, err := storage.NewClient(ctx, option.WithCredentialsFile(serviceAccount))
	if err != nil {
		return nil, fmt.Errorf("error in connecting to google storage: %w", err)
	}

	return storageClient, nil
}

// converts a struct to go map
func StructToMap(data interface{}) (map[string]interface{}, error) {
	dataBytes, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("error in converting struct to map: %w", err)
	}
	mapData := make(map[string]interface{})
	err = json.Unmarshal(dataBytes, &mapData)
	if err != nil {
		return nil, fmt.Errorf("error in unmarshaling to JSON: %w", err)
	}

	return mapData, nil
}

// acknowledges a stream segment from the redis stream
func AckStreamSegment(_ *config.Config, redisClient *redis.Client, segmentLength int, streamKey, consumerGroup string, streamIDs []string) error {
	if len(streamIDs) == segmentLength {
		for _, streamID := range streamIDs {
			redisClient.XAck(streamKey, consumerGroup, streamID)
		}

		return nil
	}

	return fmt.Errorf("failed to match streamIDs length to segment length config")
}

// looks up flag env that is a string
func LookupEnvOrString(key string, defaultVal string) string {
	if val, ok := os.LookupEnv(key); ok {
		return val
	}

	return defaultVal
}

// looks up flag env that is an integer
func LookupEnvOrInt(key string, defaultVal int) int {
	if val, ok := os.LookupEnv(key); ok {
		v, err := strconv.Atoi(val)
		if err != nil {
			log.Fatalf("LookupEnvOrInt[%s]: %v", key, err)
		}

		return v
	}

	return defaultVal
}

// gets the config from the config packages
func GetConfig(fs *flag.FlagSet) []string {
	cfg := make([]string, 0, 10)
	fs.VisitAll(func(f *flag.Flag) {
		cfg = append(cfg, fmt.Sprintf("%s:%q", f.Name, f.Value.String()))
	})

	return cfg
}

// encode returns a byte slice representing the binary encoding of the input avro record
func EncodeAvro(record avro.AvroRecord) ([]byte, error) {
	writer := avro.NewSpecificDatumWriter()
	writer.SetSchema(record.Schema())

	buffer := new(bytes.Buffer)
	encoder := avro.NewBinaryEncoder(buffer)

	err := writer.Write(record, encoder)
	if err != nil {
		return nil, fmt.Errorf("error in encoding to AVRO: %w", err)
	}

	return buffer.Bytes(), nil
}

// decode tries to decode a data buffer, read it and store it on the input record.
// If successfully, the record is filled with data from the buffer, otherwise an error might be returned
func DecodeAvro(record avro.AvroRecord, buffer []byte) error {
	reader := avro.NewSpecificDatumReader()
	reader.SetSchema(record.Schema())
	decoder := avro.NewBinaryDecoder(buffer)

	return fmt.Errorf("error in decoding AVRO: %w", reader.Read(record, decoder))
}
