package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/dx"
)

func getConfig(cmd *command, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("config key required")
	}
	config, err := core.LoadConfig("")
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	key := args[0]
	value, ok := readConfigValue(config, key)
	if !ok {
		return fmt.Errorf("unknown config key: %s", key)
	}
	cliOutput().Println(value)
	return nil
}

func setConfig(cmd *command, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("config key and value required")
	}

	config, err := core.LoadConfig("")
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	if err := updateConfigValue(config, args[0], args[1]); err != nil {
		return err
	}
	if err := config.Save(); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}
	cliOutput().Status(dx.Success, "Configuration updated")
	return nil
}

func readConfigValue(config *core.Config, key string) (any, bool) {
	if value, ok := storageConfigValue(config, key); ok {
		return value, true
	}
	if value, ok := daemonConfigValue(config, key); ok {
		return value, true
	}
	if value, ok := apiConfigValue(config, key); ok {
		return value, true
	}
	return monitoringConfigValue(config, key)
}

func storageConfigValue(config *core.Config, key string) (any, bool) {
	switch key {
	case "storage.json_file":
		return config.Storage.JSONFile, true
	case "storage.retention_days":
		return config.Storage.RetentionDays, true
	case "storage.max_executions":
		return config.Storage.MaxExecutions, true
	case "storage.max_storage_bytes":
		return config.Storage.MaxStorageBytes, true
	case "storage.max_backups":
		return config.Storage.MaxBackups, true
	}
	return nil, false
}

func daemonConfigValue(config *core.Config, key string) (any, bool) {
	switch key {
	case "daemon.pid_file":
		return config.Daemon.PIDFile, true
	case "daemon.socket_path":
		return config.Daemon.SocketPath, true
	}
	return nil, false
}

func apiConfigValue(config *core.Config, key string) (any, bool) {
	switch key {
	case "api.enabled":
		return config.API.IsEnabled, true
	case "api.port":
		return config.API.Port, true
	}
	return nil, false
}

func monitoringConfigValue(config *core.Config, key string) (any, bool) {
	switch key {
	case "monitoring.enabled_tools":
		return strings.Join(config.Monitoring.EnabledTools, ", "), true
	}
	return nil, false
}

func updateConfigValue(config *core.Config, key, value string) error {
	if updated, err := updateStorageConfigValue(config, key, value); updated {
		return err
	}
	if updated := updateTextConfigValue(config, key, value); updated {
		return nil
	}
	return updateParsedConfigValue(config, key, value)
}

func updateStorageConfigValue(config *core.Config, key, value string) (bool, error) {
	switch key {
	case "storage.json_file":
		config.Storage.JSONFile = value
		return true, nil
	case "storage.retention_days":
		return updateRetentionDays(config, value)
	case "storage.max_executions":
		return updateMaxExecutions(config, value)
	case "storage.max_storage_bytes":
		return updateMaxStorageBytes(config, value)
	case "storage.max_backups":
		return updateMaxBackups(config, value)
	}
	return false, nil
}

func updateRetentionDays(config *core.Config, value string) (bool, error) {
	parsed, err := parseNonNegativeInt("retention_days", value)
	if err != nil {
		return true, err
	}
	config.Storage.RetentionDays = parsed
	return true, nil
}

func updateMaxExecutions(config *core.Config, value string) (bool, error) {
	parsed, err := parseNonNegativeInt("max_executions", value)
	if err != nil {
		return true, err
	}
	config.Storage.MaxExecutions = parsed
	return true, nil
}

func updateMaxStorageBytes(config *core.Config, value string) (bool, error) {
	parsed, err := parseNonNegativeInt64("max_storage_bytes", value)
	if err != nil {
		return true, err
	}
	config.Storage.MaxStorageBytes = parsed
	return true, nil
}

func updateMaxBackups(config *core.Config, value string) (bool, error) {
	parsed, err := parseNonNegativeInt("max_backups", value)
	if err != nil {
		return true, err
	}
	config.Storage.MaxBackups = parsed
	return true, nil
}

func updateTextConfigValue(config *core.Config, key, value string) bool {
	switch key {
	case "daemon.pid_file":
		config.Daemon.PIDFile = value
	case "daemon.socket_path":
		config.Daemon.SocketPath = value
	case "monitoring.enabled_tools":
		config.Monitoring.EnabledTools = strings.Split(value, ",")
	default:
		return false
	}
	return true
}

func updateParsedConfigValue(config *core.Config, key, value string) error {
	switch key {
	case "api.enabled":
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("invalid boolean value: %w", err)
		}
		config.API.IsEnabled = parsed
	case "api.port":
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid port value: %w", err)
		}
		config.API.Port = parsed
	default:
		return fmt.Errorf("unknown config key: %s", key)
	}
	return nil
}

func parseNonNegativeInt(name, value string) (int, error) {
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("invalid %s value: %w", name, err)
	}
	if parsed < 0 {
		return 0, fmt.Errorf("%s must be non-negative", name)
	}
	return parsed, nil
}

func parseNonNegativeInt64(name, value string) (int64, error) {
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid %s value: %w", name, err)
	}
	if parsed < 0 {
		return 0, fmt.Errorf("%s must be non-negative", name)
	}
	return parsed, nil
}

func listConfig(cmd *command, args []string) error {
	config, err := core.LoadConfig("")
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	enc := json.NewEncoder(cliOutput().Stdout())
	enc.SetIndent("", "  ")
	return enc.Encode(config)
}
