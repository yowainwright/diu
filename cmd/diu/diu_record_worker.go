package main

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"syscall"
	"time"

	"github.com/yowainwright/diu/internal/core"
)

func runRecorderWorker(config *core.Config, _ *os.File, _ int) error {
	stop, err := recorderDeadline()
	if err != nil {
		return err
	}
	defer stop()
	payload, err := readRecorderPayload(os.Stdin)
	if err != nil {
		return err
	}
	if sendRecorderPayload(config.Daemon.SocketPath, payload) == nil {
		return nil
	}
	return withFallbackRecordLock(config, func(wait time.Duration) error {
		return storeFallbackExecutionFrom(config, wait, bytes.NewReader(payload))
	})
}

func recorderDeadline() (func(), error) {
	pid := os.Getpid()
	group, err := syscall.Getpgid(pid)
	hasGroupError := err != nil
	isPrivateGroup := group == pid
	invalidGroup := hasGroupError || !isPrivateGroup
	if invalidGroup {
		return nil, fmt.Errorf("recorder requires a private process group")
	}
	timer := time.AfterFunc(core.RecorderTimeout, func() { _ = killRecorderGroup(pid) })
	return func() { timer.Stop() }, nil
}

func readRecorderPayload(input io.Reader) ([]byte, error) {
	payload, err := io.ReadAll(io.LimitReader(input, core.MaxRecorderPayloadBytes+1))
	if err != nil {
		return nil, err
	}
	if len(payload) > core.MaxRecorderPayloadBytes {
		return nil, fmt.Errorf("recorder payload exceeds size limit")
	}
	return payload, nil
}

func sendRecorderPayload(socket string, payload []byte) error {
	connection, err := net.DialTimeout("unix", socket, time.Second)
	if err != nil {
		return err
	}
	defer func() { _ = connection.Close() }()
	if err := connection.SetWriteDeadline(time.Now().Add(core.RecorderTimeout)); err != nil {
		return err
	}
	return writeRecorderPayload(connection, payload)
}

func writeRecorderPayload(connection net.Conn, payload []byte) error {
	for len(payload) > 0 {
		written, err := connection.Write(payload)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		payload = payload[written:]
	}
	return nil
}
