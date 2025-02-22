package v2

import (
	"context"
	"encoding/hex"
	"fmt"
	"strconv"
	"time"

	"github.com/attestantio/go-eth2-client/spec"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	backoff "github.com/cenkalti/backoff/v4"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethpandaops/xatu/pkg/cannon/ethereum"
	"github.com/ethpandaops/xatu/pkg/cannon/iterator"
	"github.com/ethpandaops/xatu/pkg/observability"
	xatuethv1 "github.com/ethpandaops/xatu/pkg/proto/eth/v1"
	"github.com/ethpandaops/xatu/pkg/proto/xatu"
	"github.com/google/uuid"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

type ExecutionTransactionDeriver struct {
	log               logrus.FieldLogger
	cfg               *ExecutionTransactionDeriverConfig
	iterator          *iterator.CheckpointIterator
	onEventsCallbacks []func(ctx context.Context, events []*xatu.DecoratedEvent) error
	beacon            *ethereum.BeaconNode
	clientMeta        *xatu.ClientMeta
}

type ExecutionTransactionDeriverConfig struct {
	Enabled bool `yaml:"enabled" default:"true"`
}

const (
	ExecutionTransactionDeriverName = xatu.CannonType_BEACON_API_ETH_V2_BEACON_BLOCK_EXECUTION_TRANSACTION
)

func NewExecutionTransactionDeriver(log logrus.FieldLogger, config *ExecutionTransactionDeriverConfig, iter *iterator.CheckpointIterator, beacon *ethereum.BeaconNode, clientMeta *xatu.ClientMeta) *ExecutionTransactionDeriver {
	return &ExecutionTransactionDeriver{
		log:        log.WithField("module", "cannon/event/beacon/eth/v2/execution_transaction"),
		cfg:        config,
		iterator:   iter,
		beacon:     beacon,
		clientMeta: clientMeta,
	}
}

func (b *ExecutionTransactionDeriver) CannonType() xatu.CannonType {
	return ExecutionTransactionDeriverName
}

func (b *ExecutionTransactionDeriver) Name() string {
	return ExecutionTransactionDeriverName.String()
}

func (b *ExecutionTransactionDeriver) OnEventsDerived(ctx context.Context, fn func(ctx context.Context, events []*xatu.DecoratedEvent) error) {
	b.onEventsCallbacks = append(b.onEventsCallbacks, fn)
}

func (b *ExecutionTransactionDeriver) Start(ctx context.Context) error {
	if !b.cfg.Enabled {
		b.log.Info("Execution transaction deriver disabled")

		return nil
	}

	b.log.Info("Execution transaction deriver enabled")

	// Start our main loop
	go b.run(ctx)

	return nil
}

func (b *ExecutionTransactionDeriver) Stop(ctx context.Context) error {
	return nil
}

func (b *ExecutionTransactionDeriver) run(rctx context.Context) {
	bo := backoff.NewExponentialBackOff()
	bo.MaxInterval = 3 * time.Minute

	for {
		select {
		case <-rctx.Done():
			return
		default:
			operation := func() error {
				ctx, span := observability.Tracer().Start(rctx, fmt.Sprintf("Derive %s", b.Name()),
					trace.WithAttributes(
						attribute.String("network", string(b.beacon.Metadata().Network.Name))),
				)
				defer span.End()

				time.Sleep(100 * time.Millisecond)

				if err := b.beacon.Synced(ctx); err != nil {
					return err
				}

				// Get the next slot
				location, lookAhead, err := b.iterator.Next(ctx)
				if err != nil {
					return err
				}

				// Look ahead
				b.lookAheadAtLocation(ctx, lookAhead)

				// Process the epoch
				events, err := b.processEpoch(ctx, phase0.Epoch(location.GetEthV2BeaconBlockExecutionTransaction().GetEpoch()))
				if err != nil {
					b.log.WithError(err).Error("Failed to process epoch")

					return err
				}

				// Send the events
				for _, fn := range b.onEventsCallbacks {
					if err := fn(ctx, events); err != nil {
						return errors.Wrap(err, "failed to send events")
					}
				}

				// Update our location
				if err := b.iterator.UpdateLocation(ctx, location); err != nil {
					return err
				}

				bo.Reset()

				return nil
			}

			if err := backoff.RetryNotify(operation, bo, func(err error, timer time.Duration) {
				b.log.WithError(err).WithField("next_attempt", timer).Warn("Failed to process")
			}); err != nil {
				b.log.WithError(err).Warn("Failed to process")
			}
		}
	}
}

func (b *ExecutionTransactionDeriver) processEpoch(ctx context.Context, epoch phase0.Epoch) ([]*xatu.DecoratedEvent, error) {
	ctx, span := observability.Tracer().Start(ctx,
		"ExecutionTransactionDeriver.processEpoch",
		trace.WithAttributes(attribute.Int64("epoch", int64(epoch))),
	)
	defer span.End()

	sp, err := b.beacon.Node().Spec()
	if err != nil {
		return nil, errors.Wrap(err, "failed to obtain spec")
	}

	allEvents := []*xatu.DecoratedEvent{}

	for i := uint64(0); i <= uint64(sp.SlotsPerEpoch-1); i++ {
		slot := phase0.Slot(i + uint64(epoch)*uint64(sp.SlotsPerEpoch))

		events, err := b.processSlot(ctx, slot)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to process slot %d", slot)
		}

		allEvents = append(allEvents, events...)
	}

	return allEvents, nil
}

// lookAheadAtLocation takes the upcoming locations and looks ahead to do any pre-processing that might be required.
func (b *ExecutionTransactionDeriver) lookAheadAtLocation(ctx context.Context, locations []*xatu.CannonLocation) {
	_, span := observability.Tracer().Start(ctx,
		"ExecutionTransactionDeriver.lookAheadAtLocations",
	)
	defer span.End()

	for _, location := range locations {
		// Get the next look ahead epoch
		epoch := phase0.Epoch(location.GetEthV2BeaconBlockExecutionTransaction().GetEpoch())

		sp, err := b.beacon.Node().Spec()
		if err != nil {
			b.log.WithError(err).WithField("epoch", epoch).Warn("Failed to look ahead at epoch")

			return
		}

		for i := uint64(0); i <= uint64(sp.SlotsPerEpoch-1); i++ {
			slot := phase0.Slot(i + uint64(epoch)*uint64(sp.SlotsPerEpoch))

			// Add the block to the preload queue so it's available when we need it
			b.beacon.LazyLoadBeaconBlock(xatuethv1.SlotAsString(slot))
		}
	}
}

func (b *ExecutionTransactionDeriver) processSlot(ctx context.Context, slot phase0.Slot) ([]*xatu.DecoratedEvent, error) {
	ctx, span := observability.Tracer().Start(ctx,
		"ExecutionTransactionDeriver.processSlot",
		trace.WithAttributes(attribute.Int64("slot", int64(slot))),
	)
	defer span.End()

	// Get the block
	block, err := b.beacon.GetBeaconBlock(ctx, xatuethv1.SlotAsString(slot))
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get beacon block for slot %d", slot)
	}

	if block == nil {
		return []*xatu.DecoratedEvent{}, nil
	}

	blockIdentifier, err := GetBlockIdentifier(block, b.beacon.Metadata().Wallclock())
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get block identifier for slot %d", slot)
	}

	events := []*xatu.DecoratedEvent{}

	transactions, err := b.getExecutionTransactions(ctx, block)
	if err != nil {
		return nil, err
	}

	for index, transaction := range transactions {
		from, err := types.Sender(types.LatestSignerForChainID(transaction.ChainId()), transaction)
		if err != nil {
			return nil, fmt.Errorf("failed to get transaction sender: %v", err)
		}

		gasPrice := transaction.GasPrice()
		if gasPrice == nil {
			return nil, fmt.Errorf("failed to get transaction gas price")
		}

		value := transaction.Value()
		if value == nil {
			return nil, fmt.Errorf("failed to get transaction value")
		}

		to := ""

		if transaction.To() != nil {
			to = transaction.To().Hex()
		}

		chainID := transaction.ChainId()
		if chainID == nil {
			return nil, fmt.Errorf("failed to get transaction chain ID")
		}

		tx := &xatuethv1.Transaction{
			Nonce:    wrapperspb.UInt64(transaction.Nonce()),
			GasPrice: gasPrice.String(),
			Gas:      wrapperspb.UInt64(transaction.Gas()),
			To:       to,
			From:     from.Hex(),
			Value:    value.String(),
			Input:    hex.EncodeToString(transaction.Data()),
			Hash:     transaction.Hash().Hex(),
			ChainId:  chainID.String(),
			Type:     wrapperspb.UInt32(uint32(transaction.Type())),
		}

		event, err := b.createEvent(ctx, tx, uint64(index), blockIdentifier, transaction)
		if err != nil {
			b.log.WithError(err).Error("Failed to create event")

			return nil, errors.Wrapf(err, "failed to create event for execution transaction %s", transaction.Hash())
		}

		events = append(events, event)
	}

	return events, nil
}

func (b *ExecutionTransactionDeriver) getExecutionTransactions(ctx context.Context, block *spec.VersionedSignedBeaconBlock) ([]*types.Transaction, error) {
	transactions := []*types.Transaction{}

	txs, err := block.ExecutionTransactions()
	if err != nil {
		return nil, fmt.Errorf("failed to get execution transactions: %v", err)
	}

	for _, transaction := range txs {
		ethTransaction := new(types.Transaction)
		if err := ethTransaction.UnmarshalBinary(transaction); err != nil {
			return nil, fmt.Errorf("failed to unmarshal transaction: %v", err)
		}
	}

	return transactions, nil
}

func (b *ExecutionTransactionDeriver) createEvent(ctx context.Context, transaction *xatuethv1.Transaction, positionInBlock uint64, blockIdentifier *xatu.BlockIdentifier, rlpTransaction *types.Transaction) (*xatu.DecoratedEvent, error) {
	// Make a clone of the metadata
	metadata, ok := proto.Clone(b.clientMeta).(*xatu.ClientMeta)
	if !ok {
		return nil, errors.New("failed to clone client metadata")
	}

	decoratedEvent := &xatu.DecoratedEvent{
		Event: &xatu.Event{
			Name:     xatu.Event_BEACON_API_ETH_V2_BEACON_BLOCK_EXECUTION_TRANSACTION,
			DateTime: timestamppb.New(time.Now()),
			Id:       uuid.New().String(),
		},
		Meta: &xatu.Meta{
			Client: metadata,
		},
		Data: &xatu.DecoratedEvent_EthV2BeaconBlockExecutionTransaction{
			EthV2BeaconBlockExecutionTransaction: transaction,
		},
	}

	decoratedEvent.Meta.Client.AdditionalData = &xatu.ClientMeta_EthV2BeaconBlockExecutionTransaction{
		EthV2BeaconBlockExecutionTransaction: &xatu.ClientMeta_AdditionalEthV2BeaconBlockExecutionTransactionData{
			Block:           blockIdentifier,
			PositionInBlock: wrapperspb.UInt64(positionInBlock),
			Size:            strconv.FormatFloat(float64(rlpTransaction.Size()), 'f', 0, 64),
			CallDataSize:    fmt.Sprintf("%d", len(rlpTransaction.Data())),
		},
	}

	return decoratedEvent, nil
}
