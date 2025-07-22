package logging

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"4EYESConsulting/sigmalok-indexer/internal/config"

	"path/filepath"

	"github.com/disgoorg/dislog"
	"github.com/disgoorg/snowflake/v2"
	"github.com/natefinch/lumberjack"
	"github.com/sirupsen/logrus"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type Logger = zap.SugaredLogger

var (
	globalLogger  *zap.SugaredLogger
	discordLogger *logrus.Logger
	discordHook   *dislog.DisLog
)

func Setup(cfg *config.Config) error {

	baseLogDir := cfg.Logging.LogDirectory
	if baseLogDir == "" {
		baseLogDir = "assets/logs"
	}

	if err := os.MkdirAll(baseLogDir, 0755); err != nil {
		return fmt.Errorf("error creating log directory: %w", err)
	}

	logFileName := cfg.Logging.LogFileName
	if logFileName == "" {
		logFileName = "app.log"
	}

	// Construct the full path for the log file
	logFilePath := filepath.Join(baseLogDir, logFileName)

	// Create a custom encoder config
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.TimeKey = "timestamp"
	encoderConfig.EncodeTime = zapcore.TimeEncoderOfLayout(time.RFC3339)

	jsonEncoder := zapcore.NewJSONEncoder(encoderConfig)

	// Parse the desired log level
	level := zap.NewAtomicLevelAt(zapcore.InfoLevel)
	if cfg.Logging.Level != "" {
		if err := level.UnmarshalText([]byte(cfg.Logging.Level)); err != nil {
			return fmt.Errorf("error parsing log level: %w", err)
		}
	}

	// Create a custom core that writes JSON to both rotated file and console
	core := zapcore.NewTee(
		zapcore.NewCore(
			jsonEncoder,
			getRotatedFileWriter(
				logFilePath,
				cfg.Logging.MaxLogFileSize,
				cfg.Logging.MaxBackups,
				cfg.Logging.MaxAge,
			),
			level,
		),
		zapcore.NewCore(jsonEncoder, zapcore.AddSync(os.Stdout), level),
	)

	logger := zap.New(core)

	globalLogger = logger.Sugar()
	return nil
}

func getRotatedFileWriter(
	filename string,
	maxSize, maxBackups, maxAge int,
) zapcore.WriteSyncer {
	return zapcore.AddSync(&lumberjack.Logger{
		Filename:   filename,
		MaxSize:    maxSize,
		MaxBackups: maxBackups,
		MaxAge:     maxAge,
		Compress:   true,
		LocalTime:  true,
	})
}

func GetLogger() *zap.SugaredLogger {
	return globalLogger
}

func GetDiscordLogger(cfg *config.Config) (*logrus.Logger, error) {
	if discordLogger != nil {
		return discordLogger, nil // Discord logger already set up
	}

	discordLogger = logrus.New()

	// Use the configured log file name with a "discord_" prefix, or default to "discord_logs.log" if not specified
	discordLogFileName := "discord_logs.log"
	if cfg.Logging.LogFileName != "" {
		discordLogFileName = "discord_" + cfg.Logging.LogFileName
	}

	// Construct the full path for the discord log file
	baseLogDir := "assets/logs"
	discordLogFilePath := filepath.Join(baseLogDir, discordLogFileName)

	// Ensure the log directory exists
	if err := os.MkdirAll(baseLogDir, 0755); err != nil {
		return nil, fmt.Errorf("error creating log directory: %w", err)
	}

	// Create discord log file
	logFile, err := os.OpenFile(
		discordLogFilePath,
		os.O_CREATE|os.O_WRONLY|os.O_APPEND,
		0644,
	)
	if err != nil {
		return nil, fmt.Errorf("error opening %s: %w", discordLogFileName, err)
	}

	// Set up multi-writer for both file and stdout
	mw := io.MultiWriter(os.Stdout, logFile)
	discordLogger.SetOutput(mw)

	// Configure Discord logging
	dislog.TraceLevelColor = 0xd400ff
	dislog.LogWait = 1 * time.Second
	dislog.TimeFormatter = "2006-01-02 15:04:05 Z07"

	snowflakeId, token, err := newWithURL(cfg.Logging.LogDiscordWebookURL)
	if err != nil {
		return nil, fmt.Errorf("error parsing webhook url: %w", err)
	}

	hook, err := dislog.New(
		dislog.WithLogLevels(dislog.TraceLevelAndAbove...),
		dislog.WithWebhookIDToken(snowflakeId, token),
	)
	if err != nil {
		return nil, fmt.Errorf("error creating discord hook: %w", err)
	}

	discordHook = hook
	discordLogger.AddHook(hook)

	return discordLogger, nil
}

func CloseDiscordLogger() {
	if discordHook != nil {
		discordHook.Close(context.Background())
	}
}

func newWithURL(webhookURL string) (snowflake.ID, string, error) {
	if webhookURL == "" {
		return snowflake.New(time.Now()), "", fmt.Errorf("webhook URL is empty")
	}

	u, err := url.Parse(webhookURL)
	if err != nil {
		return snowflake.New(
				time.Now(),
			), "", fmt.Errorf(
				"invalid webhook URL: %w",
				err,
			)
	}

	parts := strings.FieldsFunc(u.Path, func(r rune) bool { return r == '/' })
	if len(parts) != 4 {
		return snowflake.New(
				time.Now(),
			), "", fmt.Errorf(
				"invalid webhook URL format: %s",
				u.String(),
			)
	}

	token := parts[3]
	id, err := snowflake.Parse(parts[2])
	if err != nil {
		return snowflake.New(
				time.Now(),
			), "", fmt.Errorf(
				"invalid webhook ID: %w",
				err,
			)
	}

	return id, token, nil
}
