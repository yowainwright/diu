package main

import (
	"errors"
	"fmt"
	"time"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/dx"
	"github.com/yowainwright/diu/internal/storage"
)

var setupBackgroundTracking = installLaunchAgent

func setupProject(cmd *command, args []string) error {
	activity := cliOutput().StartActivity("Setting up DIU")
	defer activity.Stop()

	if err := runSetupProject(activity); err != nil {
		return err
	}
	activity.Success("DIU setup completed")
	return nil
}

func runSetupProject(activity *dx.Activity) error {
	config, err := loadSetupConfig()
	if err != nil {
		return err
	}
	wasRunning, err := stopDaemonWithState(config, waitForSetupDaemonExit)
	if err != nil {
		return err
	}
	err = configureSetupProject(config, activity)
	return restoreRecorderAfterSetupFailure(config, wasRunning, err)
}

func waitForSetupDaemonExit(config *core.Config, pid int, pidErr error) error {
	stopErr := waitForDaemonExit(config, pid, pidErr)
	if stopErr == nil {
		return nil
	}
	cliOutput().Status(dx.Warning, "Setup aborted; waiting once more for recorder shutdown before recovery")
	return recoverRecorderAfterStopTimeout(config, pid, pidErr, stopErr)
}

func recoverRecorderAfterStopTimeout(config *core.Config, pid int, pidErr, stopErr error) error {
	if err := waitForDaemonExit(config, pid, pidErr); err != nil {
		recoveryErr := fmt.Errorf("recorder shutdown is unconfirmed; once it exits, run 'diu daemon start' to restore recording, then retry 'diu setup': %w", err)
		return errors.Join(stopErr, recoveryErr)
	}
	wasRunning := true
	return restoreRecorderAfterSetupFailure(config, wasRunning, stopErr)
}

func restoreRecorderAfterSetupFailure(config *core.Config, wasRunning bool, setupErr error) error {
	shouldRestore := wasRunning && setupErr != nil
	if !shouldRestore {
		return setupErr
	}
	if err := startDaemonWithConfig(config); err != nil {
		restoreErr := fmt.Errorf("failed to restore recorder after setup failure: %w", err)
		return errors.Join(setupErr, restoreErr)
	}
	return setupErr
}

func configureSetupProject(config *core.Config, activity *dx.Activity) error {
	if err := initializeSetupStorage(config); err != nil {
		return err
	}

	warn := func(message string) { activity.Notice(dx.Warning, message) }
	if err := configureCommandWrappers(config, warn); err != nil {
		return err
	}
	if _, err := scanInventory(config, activity); err != nil {
		return err
	}
	return setupBackgroundTracking(config)
}

func loadSetupConfig() (*core.Config, error) {
	config, err := core.LoadConfig("")
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}
	if err := config.EnsureDirectories(); err != nil {
		return nil, err
	}
	if err := config.Save(); err != nil {
		return nil, fmt.Errorf("failed to save config: %w", err)
	}
	return config, nil
}

func initializeSetupStorage(config *core.Config) error {
	store, err := storage.NewJSONStorage(config)
	if err != nil {
		return fmt.Errorf("failed to initialize storage: %w", err)
	}
	if err := store.Close(); err != nil {
		return fmt.Errorf("failed to close storage: %w", err)
	}
	return nil
}

func uninstallProject(cmd *command, args []string) error {
	activity := cliOutput().StartActivity("Removing DIU setup")
	defer activity.Stop()
	paths, err := loadUninstallPaths()
	if err != nil {
		return err
	}
	if err := removeSetupArtifacts(paths, uninstallBackgroundTracking); err != nil {
		return err
	}
	activity.Success("DIU setup removed; configuration and usage data preserved")
	return nil
}

func cleanup(cmd *command, args []string) error {
	activity := cliOutput().StartActivity("Cleaning execution history")
	defer activity.Stop()
	if err := runCleanup(activity); err != nil {
		return err
	}
	activity.Success("Cleanup completed")
	return nil
}

func runCleanup(activity *dx.Activity) error {
	config, err := core.LoadConfig("")
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	store, err := storage.NewJSONStorage(config)
	if err != nil {
		return fmt.Errorf("failed to open storage: %w", err)
	}
	defer closeStoreDuringActivity(store, activity)

	if err := store.Cleanup(time.Time{}); err != nil {
		return fmt.Errorf("cleanup failed: %w", err)
	}
	return nil
}

func backup(cmd *command, args []string) error {
	activity := cliOutput().StartActivity("Creating backup")
	defer activity.Stop()
	if err := runBackup(activity); err != nil {
		return err
	}
	activity.Success("Backup created")
	return nil
}

func runBackup(activity *dx.Activity) error {
	config, err := core.LoadConfig("")
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	store, err := storage.NewJSONStorage(config)
	if err != nil {
		return fmt.Errorf("failed to open storage: %w", err)
	}
	defer closeStoreDuringActivity(store, activity)

	if err := store.Backup(); err != nil {
		return fmt.Errorf("backup failed: %w", err)
	}
	return nil
}
