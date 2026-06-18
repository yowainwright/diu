package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/yowainwright/diu/internal/core"
)

// getConfig gets a configuration value
func getConfig(cmd *command, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("config key required")
	}

	config, err := core.LoadConfig("")
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	key := args[0]
	switch key {
	case "storage.json_file":
		fmt.Println(config.Storage.JSONFile)
	case "storage.retention_days":
		fmt.Println(config.Storage.RetentionDays)
	case "storage.max_executions":
		fmt.Println(config.Storage.MaxExecutions)
	case "storage.max_storage_bytes":
		fmt.Println(config.Storage.MaxStorageBytes)
	case "storage.max_backups":
		fmt.Println(config.Storage.MaxBackups)
	case "daemon.pid_file":
		fmt.Println(config.Daemon.PIDFile)
	case "daemon.socket_path":
		fmt.Println(config.Daemon.SocketPath)
	case "api.enabled":
		fmt.Println(config.API.Enabled)
	case "api.port":
		fmt.Println(config.API.Port)
	case "monitoring.enabled_tools":
		fmt.Println(strings.Join(config.Monitoring.EnabledTools, ", "))
	default:
		return fmt.Errorf("unknown config key: %s", key)
	}

	return nil
}

// setConfig sets a configuration value
func setConfig(cmd *command, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("config key and value required")
	}

	config, err := core.LoadConfig("")
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	key := args[0]
	value := args[1]

	switch key {
	case "storage.json_file":
		config.Storage.JSONFile = value
	case "storage.retention_days":
		days, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid retention_days value: %w", err)
		}
		if days < 0 {
			return fmt.Errorf("retention_days must be non-negative")
		}
		config.Storage.RetentionDays = days
	case "storage.max_executions":
		maxExecutions, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid max_executions value: %w", err)
		}
		if maxExecutions < 0 {
			return fmt.Errorf("max_executions must be non-negative")
		}
		config.Storage.MaxExecutions = maxExecutions
	case "storage.max_storage_bytes":
		maxStorageBytes, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid max_storage_bytes value: %w", err)
		}
		if maxStorageBytes < 0 {
			return fmt.Errorf("max_storage_bytes must be non-negative")
		}
		config.Storage.MaxStorageBytes = maxStorageBytes
	case "storage.max_backups":
		maxBackups, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid max_backups value: %w", err)
		}
		if maxBackups < 0 {
			return fmt.Errorf("max_backups must be non-negative")
		}
		config.Storage.MaxBackups = maxBackups
	case "daemon.pid_file":
		config.Daemon.PIDFile = value
	case "daemon.socket_path":
		config.Daemon.SocketPath = value
	case "api.enabled":
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("invalid boolean value: %w", err)
		}
		config.API.Enabled = enabled
	case "api.port":
		port, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid port value: %w", err)
		}
		config.API.Port = port
	case "monitoring.enabled_tools":
		config.Monitoring.EnabledTools = strings.Split(value, ",")
	default:
		return fmt.Errorf("unknown config key: %s", key)
	}

	if err := config.Save(); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	fmt.Println(successStyle.Render("Configuration updated"))
	return nil
}

// listConfig lists all configuration
func listConfig(cmd *command, args []string) error {
	config, err := core.LoadConfig("")
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(config)
}
