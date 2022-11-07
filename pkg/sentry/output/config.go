package output

import (
	"errors"

	"github.com/ethpandaops/xatu/pkg/sentry/output/http"
	"github.com/sirupsen/logrus"
)

type Config struct {
	SinkType SinkType `yaml:"type"`

	Config *RawMessage `yaml:"config"`
}

func (c *Config) Validate() error {
	if c.SinkType == SinkTypeUnknown {
		return errors.New("sink type is required")
	}

	return nil
}

func NewSink(sinkType SinkType, config *RawMessage, log logrus.FieldLogger) (Sink, error) {
	if sinkType == SinkTypeUnknown {
		return nil, errors.New("sink type is required")
	}

	switch sinkType {
	case SinkTypeHTTP:
		conf := &http.Config{}

		if err := config.Unmarshal(conf); err != nil {
			return nil, err
		}

		return http.New(conf, log)
	default:
		return nil, errors.New("sink type is unknown")
	}
}
