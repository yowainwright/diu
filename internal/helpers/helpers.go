package helpers

import (
	"bufio"
	"context"
	"errors"
	"io"
	"time"
)

func Map[T, R any](items []T, transform func(T) R) []R {
	result := make([]R, len(items))
	for index, item := range items {
		result[index] = transform(item)
	}
	return result
}

func Filter[T any](items []T, keep func(T) bool) []T {
	var result []T
	for _, item := range items {
		if keep(item) {
			result = append(result, item)
		}
	}
	return result
}

func FilterMap[T, R any](items []T, transform func(T) (R, bool)) []R {
	result := make([]R, 0, len(items))
	for _, item := range items {
		value, keep := transform(item)
		if keep {
			result = append(result, value)
		}
	}
	return result
}

func ScanLines[T any](reader io.Reader, parse func(string) (T, bool)) ([]T, error) {
	var result []T
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		value, keep := parse(scanner.Text())
		if keep {
			result = append(result, value)
		}
	}
	return result, scanner.Err()
}

func Sleep(ctx context.Context, delay time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ctx.Err()
	}
}

func WaitUntil(ctx context.Context, interval time.Duration, ready func() bool) error {
	if interval <= 0 {
		return errors.New("poll interval must be positive")
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if ready() {
			return nil
		}
		if err := Sleep(ctx, interval); err != nil {
			return err
		}
	}
}
