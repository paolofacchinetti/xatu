package clickhouse

import (
	"errors"
)

type Config struct {
	Address          []string            `yaml:"address"`
	Database         string              `yaml:"database" default:"default"`
	Username         string              `yaml:"username" default:"default"`
	Password         string              `yaml:"password"`
	Debug            bool                `yaml:"debug" default:"false"`
	MaxExecutionTime int                 `yaml:"maxExecutionTime" default:"60"`
	Compression      CompressionStrategy `yaml:"compression" default:"none"`
	// CompressionLevel is only required by brotli and zlib CompressionStrategies
	CompressionLevel      int  `yaml:"compressionLevel" default:"3"`
	TLSInsecureSkipVerify bool `yaml:"tlsInsecureSkipVerify" default:"false"`
}

func (c *Config) Validate() error {
	if len(c.Address) == 0 {
		return errors.New("at least one address is required")
	}

	// TODO: additional validation

	return nil
}
