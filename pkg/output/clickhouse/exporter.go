package clickhouse

import (
	"context"
	"database/sql"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ethpandaops/xatu/pkg/proto/xatu"
	"github.com/sirupsen/logrus"
)

type ItemExporter struct {
	config *Config
	log    logrus.FieldLogger
	db     *sql.DB
}

func NewItemExporter(name string, config *Config, log logrus.FieldLogger) (ItemExporter, error) {
	opts := buildClickhouseOptions(config)
	chDb := clickhouse.OpenDB(opts)

	return ItemExporter{
		config: config,
		log:    log.WithField("output_name", name).WithField("output_type", SinkType),
		db:     chDb,
	}, nil
}

func (e ItemExporter) ExportItems(ctx context.Context, items []*xatu.DecoratedEvent) error {
	// TODO

	return nil
}

func (e ItemExporter) Shutdown(ctx context.Context) error {
	return nil
}
