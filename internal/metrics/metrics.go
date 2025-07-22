package metrics

import (
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// Indexer metrics - tracking blockchain synchronization
	MetricSlot = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "indexer_indexer_slot",
		Help: "indexer current slot number",
	})
	MetricTipSlot = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "indexer_indexer_tip_slot",
		Help: "Slot number for upstream chain tip",
	})
	MetricTipReached = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "indexer_indexer_tip_reached",
		Help: "Whether the indexer has reached the chain tip (1 = reached, 0 = syncing)",
	})

	// Strategy metrics - tracking actor system health
	MetricActiveStrategies = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "indexer_active_strategies",
		Help: "Number of currently active strategy actors",
	})
	MetricStrategyRestarts = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "indexer_strategy_restarts_total",
		Help: "Total number of strategy actor restarts",
	}, []string{"strategy_id", "strategy_kind"})

	// Block processing metrics
	MetricBlocksProcessed = promauto.NewCounter(prometheus.CounterOpts{
		Name: "indexer_blocks_processed_total",
		Help: "Total number of blocks processed by the chain event processor",
	})
	MetricTransactionsProcessed = promauto.NewCounter(prometheus.CounterOpts{
		Name: "indexer_transactions_processed_total",
		Help: "Total number of transactions processed by the chain event processor",
	})
	MetricRollbacksProcessed = promauto.NewCounter(prometheus.CounterOpts{
		Name: "indexer_rollbacks_processed_total",
		Help: "Total number of rollback events processed",
	})

	// Events processed metrics
	MetricEventsProcessed = promauto.NewCounter(prometheus.CounterOpts{
		Name: "indexer_events_processed_total",
		Help: "Total number of events processed",
	})

	// Actor system metrics
	MetricActorMessages = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "indexer_actor_messages_total",
		Help: "Total number of messages processed by actors",
	}, []string{"actor_type", "message_type"})
	MetricActorRestarts = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "indexer_actor_restarts_total",
		Help: "Total number of actor restarts",
	}, []string{"actor_type", "actor_id"})

	// Error metrics
	MetricProcessingErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "indexer_processing_errors_total",
		Help: "Total number of processing errors",
	}, []string{"component", "error_type"})

	// Performance metrics
	MetricProcessingDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "indexer_processing_duration_seconds",
			Help:    "Time spent processing different types of events",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"component", "operation"},
	)

	// Indexer restart circuit breaker metrics
	MetricIndexerRestarts = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "indexer_indexer_restarts_total",
		Help: "Total number of indexer restarts by type (partial_restart, full_reset)",
	}, []string{"restart_type", "reason"})

	MetricRestartCircuitBreakerActivations = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "indexer_restart_circuit_breaker_activations_total",
			Help: "Total number of times the restart circuit breaker was activated to perform full reset",
		},
	)

	// Kafka metrics
	MetricKafkaLag = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "indexer_kafka_lag",
		Help: "Current lag (in messages) for each Kafka topic/partition",
	}, []string{"topic", "partition"})

	// Buffer metrics
	MetricBufferSize = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "indexer_buffer_size",
		Help: "Current size of various in-memory buffers",
	}, []string{"buffer_name"})

	// Height tracking
	MetricLastProcessedHeight = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "indexer_last_processed_height",
		Help: "Last fully processed block height",
	})
	MetricTipHeight = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "indexer_tip_height",
		Help: "Current chain tip height",
	})

	// Reader failures
	MetricReaderRestarts = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "indexer_reader_restarts_total",
		Help: "Total number of Kafka reader restarts",
	}, []string{"topic"})

	MetricDecodeFailures = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "indexer_decode_failures_total",
		Help: "Total number of message decode failures",
	}, []string{"topic", "type"})
)

// Helper functions for common metric operations

// IncrementStrategyCount increments the active strategies counter
func IncrementStrategyCount() {
	MetricActiveStrategies.Inc()
}

// DecrementStrategyCount decrements the active strategies counter
func DecrementStrategyCount() {
	MetricActiveStrategies.Dec()
}

// RecordStrategyRestart records a strategy restart
func RecordStrategyRestart(strategyID, strategyKind string) {
	MetricStrategyRestarts.WithLabelValues(strategyID, strategyKind).Inc()
}

// UpdateSlotMetrics updates both current slot and tip slot metrics
func UpdateSlotMetrics(currentSlot, tipSlot uint64, tipReached bool) {
	MetricSlot.Set(float64(currentSlot))
	MetricTipSlot.Set(float64(tipSlot))
	if tipReached {
		MetricTipReached.Set(1)
	} else {
		MetricTipReached.Set(0)
	}
}

// RecordActorMessage records a message processed by an actor
func RecordActorMessage(actorType, messageType string) {
	MetricActorMessages.WithLabelValues(actorType, messageType).Inc()
}

// RecordActorRestart records an actor restart
func RecordActorRestart(actorType, actorID string) {
	MetricActorRestarts.WithLabelValues(actorType, actorID).Inc()
}

// RecordProcessingError records a processing error
func RecordProcessingError(component, errorType string) {
	MetricProcessingErrors.WithLabelValues(component, errorType).Inc()
}

// RecordProcessingDuration records the duration of a processing operation
func RecordProcessingDuration(component, operation string, duration float64) {
	MetricProcessingDuration.WithLabelValues(component, operation).
		Observe(duration)
}

// Helper functions for the new metrics

// RecordKafkaLag records the current lag for a topic/partition
func RecordKafkaLag(topic string, partition int, lag int64) {
	MetricKafkaLag.WithLabelValues(topic, fmt.Sprintf("%d", partition)).
		Set(float64(lag))
}

// UpdateBufferSize updates the size of a named buffer
func UpdateBufferSize(name string, size int) {
	MetricBufferSize.WithLabelValues(name).Set(float64(size))
}

// UpdateHeightMetrics updates both processed and tip height metrics
func UpdateHeightMetrics(lastProcessed, tip uint64) {
	MetricLastProcessedHeight.Set(float64(lastProcessed))
	MetricTipHeight.Set(float64(tip))
}

// RecordReaderRestart increments the restart counter for a topic
func RecordReaderRestart(topic string) {
	MetricReaderRestarts.WithLabelValues(topic).Inc()
}

// RecordDecodeFailure increments the decode failure counter
func RecordDecodeFailure(topic, msgType string) {
	MetricDecodeFailures.WithLabelValues(topic, msgType).Inc()
}
