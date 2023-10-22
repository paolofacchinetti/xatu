package clickhouse

import (
	"crypto/tls"
	"fmt"

	"github.com/ClickHouse/clickhouse-go/v2"
)

type CompressionStrategy string

const (
	CompressionStrategyNone    CompressionStrategy = "none"
	CompressionStrategyLZ4     CompressionStrategy = "lz4"
	CompressionStrategyZSTD    CompressionStrategy = "zstd"
	CompressionStrategyGZIP    CompressionStrategy = "gzip"
	CompressionStrategyDeflate CompressionStrategy = "deflate"
	CompressionStrategyBrotli  CompressionStrategy = "br"
)

func buildClickhouseOptions(config *Config) *clickhouse.Options {
	opt := &clickhouse.Options{
		Addr: config.Address,
		Auth: clickhouse.Auth{
			Database: config.Database,
			Username: config.Username,
			Password: config.Password,
		},
		Debug: config.Debug,
		Debugf: func(format string, v ...any) {
			fmt.Printf(format, v)
		},
		TLS: &tls.Config{
			InsecureSkipVerify: config.TLSInsecureSkipVerify,
		},
	}

	comp := &clickhouse.Compression{}
	comp.Level = config.CompressionLevel

	switch config.Compression {
	case CompressionStrategyZSTD:
		comp.Method = clickhouse.CompressionZSTD
	case CompressionStrategyLZ4:
		comp.Method = clickhouse.CompressionLZ4
	case CompressionStrategyGZIP:
		comp.Method = clickhouse.CompressionGZIP
	case CompressionStrategyDeflate:
		comp.Method = clickhouse.CompressionDeflate
	case CompressionStrategyBrotli:
		comp.Method = clickhouse.CompressionBrotli
	}

	opt.Compression = comp

	return opt
}
