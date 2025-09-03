package strategy

import (
	"context"
	"time"

	"github.com/anthdm/hollywood/actor"
	"github.com/disgoorg/disgo/webhook"
	"github.com/mgpai22/ergo-connector-go/node"
	"go.uber.org/zap"

	"4EYESConsulting/sigmalok-indexer/internal/config"
	"4EYESConsulting/sigmalok-indexer/internal/database"
	"4EYESConsulting/sigmalok-indexer/internal/logging"
	"4EYESConsulting/sigmalok-indexer/internal/metrics"
	"4EYESConsulting/sigmalok-indexer/internal/notifier"
	"4EYESConsulting/sigmalok-indexer/internal/types"
)

const (
	// HeightStagnationTimeout is the maximum time allowed without height increase before considering it critical
	HeightStagnationTimeout = 20 * time.Minute
)

// HealthCheckTicker is used to trigger periodic health checks
type HealthCheckTicker struct{}

// NodeHealthStatus represents the health status of the node
type NodeHealthStatus struct {
	IsHealthy    bool      `json:"is_healthy"`
	LastChecked  time.Time `json:"last_checked"`
	NodeHeight   uint64    `json:"node_height"`
	NetworkName  string    `json:"network_name"`
	ErrorMessage string    `json:"error_message,omitempty"`
	ResponseTime int64     `json:"response_time_ms"`
}

// HealthCheckActor performs periodic health checks on the Ergo node
type HealthCheckActor struct {
	config    types.StrategyConfig
	appCfg    *config.Config
	logger    *zap.SugaredLogger
	engine    *actor.Engine
	selfPID   *actor.PID
	parentPID *actor.PID // Reference to StrategyManagerActor

	db           *database.Database
	nodeProvider node.NodeProvider

	checkInterval time.Duration
	ticker        *time.Ticker

	currentStatus       NodeHealthStatus
	consecutiveFailures int
	maxFailures         int

	lastAlertTime time.Time
	alertCooldown time.Duration

	// Height stagnation tracking
	lastObservedHeight    uint64
	lastHeightUpdateTime  time.Time
	heightStagnationLimit time.Duration

	// Discord notification client
	discordClient webhook.Client
}

func NewHealthCheckStrategy(
	cfg types.StrategyConfig,
	appCfg *config.Config,
	db *database.Database,
	nodeProvider node.NodeProvider,
) actor.Producer {
	return func() actor.Receiver {
		return &HealthCheckActor{
			config: cfg,
			appCfg: appCfg,
			logger: logging.GetLogger().
				With("actor", "HealthCheck", "strategy_id", cfg.ID),
			db:                    db,
			nodeProvider:          nodeProvider,
			checkInterval:         appCfg.HealthCheck.CheckInterval,
			maxFailures:           appCfg.HealthCheck.MaxFailures,
			alertCooldown:         appCfg.HealthCheck.AlertCooldown,
			heightStagnationLimit: HeightStagnationTimeout,
			lastHeightUpdateTime:  time.Now(),
			currentStatus: NodeHealthStatus{
				IsHealthy:   true,
				LastChecked: time.Now(),
			},
		}
	}
}

func (h *HealthCheckActor) Receive(c *actor.Context) {
	switch msg := c.Message().(type) {
	case actor.Initialized:
		metrics.RecordActorMessage("HealthCheck", "Initialized")
		h.logger.Info("initializing health check strategy")
		h.engine = c.Engine()
		h.selfPID = c.PID()

	case actor.Started:
		metrics.RecordActorMessage("HealthCheck", "Started")
		h.logger.Infow("starting health check strategy",
			"check_interval", h.checkInterval,
			"max_failures", h.maxFailures)

		h.startPeriodicHealthCheck()

	case actor.Stopped:
		metrics.RecordActorMessage("HealthCheck", "Stopped")
		h.logger.Info("stopping health check strategy")

		if h.ticker != nil {
			h.ticker.Stop()
		}

	case types.SetParentPID:
		h.parentPID = msg.PID

	case HealthCheckTicker:
		h.performHealthCheck()

	default:
		h.logger.Debugw("received unhandled message", "message_type", msg)
	}
}

// startPeriodicHealthCheck initializes and starts the periodic health check ticker
func (h *HealthCheckActor) startPeriodicHealthCheck() {
	h.performHealthCheck()

	h.ticker = time.NewTicker(h.checkInterval)

	go func() {
		for range h.ticker.C {
			h.engine.Send(h.selfPID, HealthCheckTicker{})
		}
	}()
}

// performHealthCheck executes a health check against the Ergo node
func (h *HealthCheckActor) performHealthCheck() {
	h.logger.Debug("performing node health check")

	startTime := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Perform the actual health check
	nodeInfo, err := h.nodeProvider.GetNodeInfo(ctx)
	responseTime := time.Since(startTime).Milliseconds()

	currentTime := time.Now()
	status := NodeHealthStatus{
		LastChecked:  currentTime,
		ResponseTime: responseTime,
	}

	if err != nil {
		h.consecutiveFailures++
		status.IsHealthy = false
		status.ErrorMessage = err.Error()

		h.logger.Warnw("node health check failed",
			"error", err,
			"consecutive_failures", h.consecutiveFailures,
			"response_time_ms", responseTime)

		if h.consecutiveFailures >= h.maxFailures {
			h.handleCriticalFailure(status)
		}

	} else {
		// nominal
		wasUnhealthy := !h.currentStatus.IsHealthy
		h.consecutiveFailures = 0
		status.IsHealthy = true
		status.NodeHeight = uint64(*nodeInfo.FullHeight)
		status.NetworkName = *nodeInfo.Network

		heightStagnation := h.checkHeightStagnation(status.NodeHeight)
		if heightStagnation {
			h.handleHeightStagnation(status)
			status.IsHealthy = false
		}

		h.logger.Debugw("node health check successful",
			"node_height", status.NodeHeight,
			"network", status.NetworkName,
			"response_time_ms", responseTime)

		// If node was previously unhealthy, log recovery
		if wasUnhealthy && status.IsHealthy {
			h.logger.Infow("node health recovered",
				"node_height", status.NodeHeight,
				"network", status.NetworkName)
			h.recordHealthRecovery(status)
		}
	}

	h.currentStatus = status

	h.recordHealthMetrics(status)
}

// handleCriticalFailure processes critical node failures
func (h *HealthCheckActor) handleCriticalFailure(status NodeHealthStatus) {
	h.logger.Errorw("node health check critical failure threshold reached",
		"consecutive_failures", h.consecutiveFailures,
		"max_failures", h.maxFailures,
		"last_error", status.ErrorMessage)

	metrics.RecordNodeHealthCriticalFailure()

	if time.Since(h.lastAlertTime) > h.alertCooldown {
		h.sendHealthAlert(status)
		h.lastAlertTime = time.Now()
	}
}

// sendHealthAlert sends alerts about critical health failures
func (h *HealthCheckActor) sendHealthAlert(status NodeHealthStatus) {
	h.logger.Errorw("CRITICAL: Node health check failed multiple times",
		"consecutive_failures", h.consecutiveFailures,
		"error", status.ErrorMessage,
		"response_time_ms", status.ResponseTime)

	if h.discordClient == nil {
		discordClient, err := webhook.NewWithURL(
			h.appCfg.Logging.NotificationDiscordWebhookURL,
		)
		if err != nil {
			h.logger.Errorf("Failed to initialize Discord client: %v", err)
		} else {
			h.discordClient = discordClient
		}
	}

	if h.discordClient != nil {
		message := notifier.BuildNodeHealthAlert(
			h.consecutiveFailures,
			h.maxFailures,
			status.ErrorMessage,
			status.ResponseTime,
			status.NetworkName,
		)
		if _, err := h.discordClient.CreateMessage(message); err != nil {
			h.logger.Errorf("Failed to send webhook: %v", err)
		}
	}
}

// recordHealthMetrics records health check metrics
func (h *HealthCheckActor) recordHealthMetrics(status NodeHealthStatus) {
	if status.ResponseTime > 0 {
		metrics.UpdateNodeHealthResponseTime(status.ResponseTime)
	}

	metrics.UpdateNodeHealthStatus(status.IsHealthy)

	metrics.UpdateNodeHealthConsecutiveFailures(h.consecutiveFailures)

	if status.NodeHeight > 0 {
		metrics.UpdateNodeHealthHeight(status.NodeHeight)
	}
}

// recordHealthRecovery records when the node recovers from failures
func (h *HealthCheckActor) recordHealthRecovery(status NodeHealthStatus) {
	metrics.RecordNodeHealthRecovery()

	h.logger.Infow("recorded node health recovery",
		"node_height", status.NodeHeight,
		"network", status.NetworkName)
}

// GetCurrentStatus returns the current health status (for API endpoints)
func (h *HealthCheckActor) GetCurrentStatus() NodeHealthStatus {
	return h.currentStatus
}

// checkHeightStagnation checks if the node height has been stagnant for too long
func (h *HealthCheckActor) checkHeightStagnation(currentHeight uint64) bool {
	now := time.Now()

	// If this is the first height observation or height has increased
	if h.lastObservedHeight == 0 || currentHeight > h.lastObservedHeight {
		h.lastObservedHeight = currentHeight
		h.lastHeightUpdateTime = now
		// Clear stagnation duration metric when height increases
		metrics.UpdateNodeHeightStagnationDuration(0)
		return false
	}

	// Height hasn't increased, check how long it's been stagnant
	stagnationDuration := now.Sub(h.lastHeightUpdateTime)
	metrics.UpdateNodeHeightStagnationDuration(
		int64(stagnationDuration.Seconds()),
	)

	if stagnationDuration >= h.heightStagnationLimit {
		h.logger.Warnw("node height stagnation detected",
			"current_height", currentHeight,
			"last_observed_height", h.lastObservedHeight,
			"stagnation_duration", stagnationDuration,
			"limit", h.heightStagnationLimit)
		return true
	}

	return false
}

// handleHeightStagnation handles the critical condition when height stagnation is detected
func (h *HealthCheckActor) handleHeightStagnation(status NodeHealthStatus) {
	stagnationDuration := time.Since(h.lastHeightUpdateTime)

	h.logger.Errorw("CRITICAL: Node height stagnation detected",
		"current_height", status.NodeHeight,
		"last_height_update", h.lastHeightUpdateTime,
		"stagnation_duration", stagnationDuration,
		"stagnation_limit", h.heightStagnationLimit)

	metrics.RecordNodeHeightStagnation()
	metrics.RecordNodeHealthCriticalFailure()

	if time.Since(h.lastAlertTime) > h.alertCooldown {
		h.sendHeightStagnationAlert(status, stagnationDuration)
		h.lastAlertTime = time.Now()
	}
}

// sendHeightStagnationAlert sends alerts about height stagnation
func (h *HealthCheckActor) sendHeightStagnationAlert(
	status NodeHealthStatus,
	stagnationDuration time.Duration,
) {
	h.logger.Errorw("CRITICAL: Node height has not increased within threshold",
		"current_height", status.NodeHeight,
		"stagnation_duration", stagnationDuration,
		"stagnation_limit", h.heightStagnationLimit)

	if h.discordClient == nil {
		discordClient, err := webhook.NewWithURL(
			h.appCfg.Logging.NotificationDiscordWebhookURL,
		)
		if err != nil {
			h.logger.Errorf("Failed to initialize Discord client: %v", err)
		} else {
			h.discordClient = discordClient
		}
	}

	if h.discordClient != nil {
		message := notifier.BuildHeightStagnationAlert(
			status.NodeHeight,
			stagnationDuration,
			h.heightStagnationLimit,
			status.NetworkName,
		)
		if _, err := h.discordClient.CreateMessage(message); err != nil {
			h.logger.Errorf("Failed to send webhook: %v", err)
		}
	}
}
