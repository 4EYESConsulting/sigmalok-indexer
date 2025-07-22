package config

import (
	"fmt"
	"os"
	"time"

	"github.com/kelseyhightower/envconfig"
	"gopkg.in/yaml.v2"
)

type Config struct {
	Storage  StorageConfig  `yaml:"storage"`
	Indexer  IndexerConfig  `yaml:"indexer"`
	API      APIConfig      `yaml:"api"`
	Logging  Logging        `yaml:"logging"`
	Server   ServerConfig   `yaml:"server"`
	Kafka    KafkaConfig    `yaml:"kafka"`
	Contract ContractConfig `yaml:"contract"`
	Profile  string         `yaml:"profile"  envconfig:"PROFILE"`
	Network  string         `yaml:"network"  envconfig:"NETWORK"`
}

type IndexerConfig struct {
	InterceptHeight         uint64        `yaml:"interceptHeight"         envconfig:"INDEXER_INTERCEPT_HEIGHT"`
	MempoolTxExpirationTime time.Duration `yaml:"mempoolTxExpirationTime" envconfig:"INDEXER_MEMPOOL_TX_EXPIRATION_TIME"`

	RestartThreshold  int           `yaml:"restartThreshold"  envconfig:"INDEXER_RESTART_THRESHOLD"`
	RestartTimeWindow time.Duration `yaml:"restartTimeWindow" envconfig:"INDEXER_RESTART_TIME_WINDOW"`

	AllowedLag    uint64 `yaml:"allowedLag"    envconfig:"INDEXER_ALLOWED_LAG"`
	AllowedBuffer int    `yaml:"allowedBuffer" envconfig:"INDEXER_ALLOWED_BUFFER"`
}

type APIConfig struct {
	NodeURL    string `yaml:"nodeURL"    envconfig:"API_NODE_URL"`
	NodeAPIKey string `yaml:"nodeAPIKey" envconfig:"API_NODE_API_KEY"`
}

type StorageConfig struct {
	Directory string `yaml:"dir" envconfig:"STORAGE_DIR"`
}

type Logging struct {
	Level                         string `yaml:"level"`
	LogDiscordWebookURL           string `yaml:"log_discord_webook_url"`
	NotificationDiscordWebhookURL string `yaml:"notification_discord_webhook_url"`
	ExplorerBaseURI               string `yaml:"explorer_base_uri"`
	LogFileName                   string `yaml:"log_file_name"`
	LogDirectory                  string `yaml:"log_directory"`
	MaxLogFileSize                int    `yaml:"max_log_file_size"`
	MaxBackups                    int    `yaml:"max_backups"`
	MaxAge                        int    `yaml:"max_age"`
}

type ServerConfig struct {
	ListenAddress string `yaml:"listenAddress" envconfig:"SERVER_LISTEN_ADDRESS"`
	ListenPort    int    `yaml:"listenPort"    envconfig:"SERVER_LISTEN_PORT"`
	EnableDebug   bool   `yaml:"enableDebug"   envconfig:"SERVER_ENABLE_DEBUG"`
	MaxLimit      int    `yaml:"maxLimit"      envconfig:"SERVER_MAX_LIMIT"`
}

type KafkaConfig struct {
	Brokers               []string `yaml:"brokers"               envconfig:"KAFKA_BROKERS"`
	GroupID               string   `yaml:"groupId"               envconfig:"KAFKA_GROUP_ID"`
	BlockTopic            string   `yaml:"blockTopic"            envconfig:"KAFKA_BLOCK_TOPIC"`
	TxTopic               string   `yaml:"txTopic"               envconfig:"KAFKA_TX_TOPIC"`
	MempoolTopic          string   `yaml:"mempoolTopic"          envconfig:"KAFKA_MEMPOOL_TOPIC"`
	MinBytes              int      `yaml:"minBytes"              envconfig:"KAFKA_MIN_BYTES"`
	MaxBytes              int      `yaml:"maxBytes"              envconfig:"KAFKA_MAX_BYTES"`
	MaxStragglersLookback int      `yaml:"maxStragglersLookback" envconfig:"KAFKA_MAX_STRAGGLERS_LOOKBACK"`
}

type ContractConfig struct {
	Ergotree string `yaml:"ergotree"`
}

var globalConfig = &Config{}

func (c *Config) setDefaults() {
	network := "mainnet"

	if c.Storage.Directory == "" {
		c.Storage.Directory = "./" + network
	}

	if c.Indexer.MempoolTxExpirationTime == 0 {
		c.Indexer.MempoolTxExpirationTime = 10 * time.Minute
	}

	if c.Indexer.RestartThreshold == 0 {
		c.Indexer.RestartThreshold = 3
	}
	if c.Indexer.RestartTimeWindow == 0 {
		c.Indexer.RestartTimeWindow = 10 * time.Minute
	}

	if c.Logging.Level == "" {
		c.Logging.Level = "info"
	}
	if c.Logging.LogFileName == "" {
		c.Logging.LogFileName = network + ".log"
	}
	if c.Logging.LogDirectory == "" {
		c.Logging.LogDirectory = "assets/logs"
	}
	if c.Logging.MaxLogFileSize == 0 {
		c.Logging.MaxLogFileSize = 100 // 100 MB
	}
	if c.Logging.MaxBackups == 0 {
		c.Logging.MaxBackups = 3
	}
	if c.Logging.MaxAge == 0 {
		c.Logging.MaxAge = 30 // 30 days
	}
	if c.Logging.ExplorerBaseURI == "" {
		c.Logging.ExplorerBaseURI = "https://explorer.ergoplatform.com/transaction/"
	}

	if c.Server.ListenAddress == "" {
		c.Server.ListenAddress = "localhost"
	}
	if c.Server.ListenPort == 0 {
		c.Server.ListenPort = 8080
	}

	if c.Network == "" {
		c.Network = network
	}
	if c.Profile == "" {
		c.Profile = network
	}

	if c.Kafka.GroupID == "" {
		c.Kafka.GroupID = "sigmalok-indexer"
	}
	if c.Kafka.BlockTopic == "" {
		c.Kafka.BlockTopic = "blocks_topic"
	}
	if c.Kafka.TxTopic == "" {
		c.Kafka.TxTopic = "tx_topic"
	}
	if c.Kafka.MempoolTopic == "" {
		c.Kafka.MempoolTopic = "mempool_topic"
	}
	if c.Kafka.MinBytes == 0 {
		c.Kafka.MinBytes = 1
	}
	if c.Kafka.MaxBytes == 0 {
		c.Kafka.MaxBytes = 1048576 // 1MB
	}
	if len(c.Kafka.Brokers) == 0 {
		c.Kafka.Brokers = []string{"localhost:19091"}
	}
}

func Load(configFile string) (*Config, error) {
	// Set defaults first
	globalConfig.setDefaults()

	// Load config file as YAML if provided
	if configFile != "" {
		buf, err := os.ReadFile(configFile)
		if err != nil {
			return nil, fmt.Errorf("error reading config file: %s", err)
		}
		err = yaml.Unmarshal(buf, globalConfig)
		if err != nil {
			return nil, fmt.Errorf("error parsing config file: %s", err)
		}
	}

	// Apply defaults again after loading config file to fill in any missing values
	globalConfig.setDefaults()

	// Load config values from environment variables
	// We use "dummy" as the app name here to (mostly) prevent picking up env
	// vars that we hadn't explicitly specified in annotations above
	err := envconfig.Process("dummy", globalConfig)
	if err != nil {
		return nil, fmt.Errorf("error processing environment: %s", err)
	}

	return globalConfig, nil
}

// GetConfig returns the global config instance
func GetConfig() *Config {
	return globalConfig
}
