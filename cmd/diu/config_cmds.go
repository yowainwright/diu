package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/dx"
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
	value, ok := configValue(config, key)
	if !ok {
		return fmt.Errorf("unknown config key: %s", key)
	}
	cliOutput().Println(value)
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
	if err := updateConfigValue(config, key, value); err != nil {
		return err
	}
	return saveConfigUpdate(config)
}

func updateConfigValue(config *core.Config, key, value string) error {
	field, ok := findConfigField(key)
	if !ok {
		return fmt.Errorf("unknown config key: %s", key)
	}
	parsed, err := parseConfigField(field, value)
	if err != nil {
		return err
	}
	return assignConfigValue(config, field.key, parsed)
}

func saveConfigUpdate(config *core.Config) error {
	if err := config.Save(); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}
	cliOutput().Status(dx.Success, "Configuration updated")
	return nil
}

type configFieldKind string

const (
	configString     configFieldKind = "string"
	configStringList configFieldKind = "string_list"
	configBool       configFieldKind = "bool"
	configInt        configFieldKind = "int"
	configInt64      configFieldKind = "int64"
)

type configField struct {
	key         string
	name        string
	kind        configFieldKind
	nonNegative bool
}

type parsedConfigValue struct {
	text    string
	texts   []string
	boolean bool
	integer int
	bigInt  int64
}

var configSchema = []configField{
	{key: "storage.json_file", name: "json_file", kind: configString},
	{key: "storage.retention_days", name: "retention_days", kind: configInt, nonNegative: true},
	{key: "storage.max_executions", name: "max_executions", kind: configInt, nonNegative: true},
	{key: "storage.max_storage_bytes", name: "max_storage_bytes", kind: configInt64, nonNegative: true},
	{key: "storage.max_backups", name: "max_backups", kind: configInt, nonNegative: true},
	{key: "daemon.pid_file", name: "pid_file", kind: configString},
	{key: "daemon.socket_path", name: "socket_path", kind: configString},
	{key: "api.enabled", name: "enabled", kind: configBool},
	{key: "api.port", name: "port", kind: configInt},
	{key: "monitoring.enabled_tools", name: "enabled_tools", kind: configStringList},
}

func findConfigField(key string) (configField, bool) {
	for _, field := range configSchema {
		if field.key == key {
			return field, true
		}
	}
	return configField{}, false
}

func configValue(config *core.Config, key string) (any, bool) {
	if _, ok := findConfigField(key); !ok {
		return nil, false
	}
	if value, ok := storageConfigValue(config, key); ok {
		return value, true
	}
	if value, ok := daemonConfigValue(config, key); ok {
		return value, true
	}
	return apiOrMonitoringConfigValue(config, key)
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

func apiOrMonitoringConfigValue(config *core.Config, key string) (any, bool) {
	switch key {
	case "api.enabled":
		return config.API.Enabled, true
	case "api.port":
		return config.API.Port, true
	case "monitoring.enabled_tools":
		return strings.Join(config.Monitoring.EnabledTools, ", "), true
	}
	return nil, false
}

func parseConfigField(field configField, value string) (parsedConfigValue, error) {
	switch field.kind {
	case configString:
		return parsedConfigValue{text: value}, nil
	case configStringList:
		return parsedConfigValue{texts: strings.Split(value, ",")}, nil
	case configBool:
		return parseConfigBool(value)
	case configInt:
		return parseConfigInt(field, value)
	case configInt64:
		return parseConfigInt64(field, value)
	}
	return parsedConfigValue{}, fmt.Errorf("unsupported config key: %s", field.key)
}

func parseConfigBool(value string) (parsedConfigValue, error) {
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return parsedConfigValue{}, fmt.Errorf("invalid boolean value: %w", err)
	}
	return parsedConfigValue{boolean: parsed}, nil
}

func parseConfigInt(field configField, value string) (parsedConfigValue, error) {
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return parsedConfigValue{}, fmt.Errorf("invalid %s value: %w", field.name, err)
	}
	if err := validateConfigNonNegative(field, int64(parsed)); err != nil {
		return parsedConfigValue{}, err
	}
	return parsedConfigValue{integer: parsed}, nil
}

func parseConfigInt64(field configField, value string) (parsedConfigValue, error) {
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return parsedConfigValue{}, fmt.Errorf("invalid %s value: %w", field.name, err)
	}
	if err := validateConfigNonNegative(field, parsed); err != nil {
		return parsedConfigValue{}, err
	}
	return parsedConfigValue{bigInt: parsed}, nil
}

func validateConfigNonNegative(field configField, value int64) error {
	if !field.nonNegative {
		return nil
	}
	if value >= 0 {
		return nil
	}
	return fmt.Errorf("%s must be non-negative", field.name)
}

func assignConfigValue(config *core.Config, key string, value parsedConfigValue) error {
	if assignStorageConfigValue(config, key, value) {
		return nil
	}
	if assignDaemonConfigValue(config, key, value) {
		return nil
	}
	if assignAPIConfigValue(config, key, value) {
		return nil
	}
	if assignMonitoringConfigValue(config, key, value) {
		return nil
	}
	return fmt.Errorf("unknown config key: %s", key)
}

func assignStorageConfigValue(config *core.Config, key string, value parsedConfigValue) bool {
	switch key {
	case "storage.json_file":
		config.Storage.JSONFile = value.text
	case "storage.retention_days":
		config.Storage.RetentionDays = value.integer
	case "storage.max_executions":
		config.Storage.MaxExecutions = value.integer
	case "storage.max_storage_bytes":
		config.Storage.MaxStorageBytes = value.bigInt
	case "storage.max_backups":
		config.Storage.MaxBackups = value.integer
	default:
		return false
	}
	return true
}

func assignDaemonConfigValue(config *core.Config, key string, value parsedConfigValue) bool {
	switch key {
	case "daemon.pid_file":
		config.Daemon.PIDFile = value.text
	case "daemon.socket_path":
		config.Daemon.SocketPath = value.text
	default:
		return false
	}
	return true
}

func assignAPIConfigValue(config *core.Config, key string, value parsedConfigValue) bool {
	switch key {
	case "api.enabled":
		config.API.Enabled = value.boolean
	case "api.port":
		config.API.Port = value.integer
	default:
		return false
	}
	return true
}

func assignMonitoringConfigValue(config *core.Config, key string, value parsedConfigValue) bool {
	if key != "monitoring.enabled_tools" {
		return false
	}
	config.Monitoring.EnabledTools = value.texts
	return true
}

// listConfig lists all configuration
func listConfig(cmd *command, args []string) error {
	config, err := core.LoadConfig("")
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	enc := json.NewEncoder(cliOutput().Stdout())
	enc.SetIndent("", "  ")
	return enc.Encode(config)
}
