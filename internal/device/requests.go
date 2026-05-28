package device

import (
	"bytes"
	"context"
	"fmt"

	"smartwatch/internal/ble"
)

// ---------------------------------------------------------------------------
// Request types (discriminated union via interface)
// ---------------------------------------------------------------------------

type request interface{ isRequest() }

type sendReq struct {
	target ble.Protocol
	packet []byte
	reply  chan error
}

func (sendReq) isRequest() {}

type scanReq struct {
	reply chan scanReply
}

func (scanReq) isRequest() {}

type scanReply struct {
	results []ScanResult
	err     error
}

// ---------------------------------------------------------------------------
// Public API — send and scan send requests into the owner loop
// ---------------------------------------------------------------------------

func (m *Manager) SendV1(ctx context.Context, packet []byte) error {
	return m.send(ctx, ble.V1, packet)
}

func (m *Manager) SendV2(ctx context.Context, packet []byte) error {
	return m.send(ctx, ble.V2, packet)
}

func (m *Manager) send(ctx context.Context, target ble.Protocol, packet []byte) error {
	reply := make(chan error, 1)
	req := sendReq{
		target: target,
		packet: bytes.Clone(packet),
		reply:  reply,
	}

	select {
	case m.reqs <- req:
	case <-ctx.Done():
		return ctx.Err()
	}

	select {
	case err := <-reply:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Scan requests a BLE scan. Only valid when idle.
func (m *Manager) Scan(ctx context.Context) ([]ScanResult, error) {
	reply := make(chan scanReply, 1)

	select {
	case m.reqs <- scanReq{reply: reply}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	select {
	case r := <-reply:
		return r.results, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func (m *Manager) replyWithError(req request, err error) {
	switch r := req.(type) {
	case sendReq:
		select { case r.reply <- err: default: }
	case scanReq:
		select { case r.reply <- scanReply{err: err}: default: }
	}
}

// ---------------------------------------------------------------------------
// Logging helpers for packet description
// ---------------------------------------------------------------------------

func protocolName(p ble.Protocol) string {
	switch p {
	case ble.V1:
		return "v1"
	case ble.V2:
		return "v2"
	default:
		return fmt.Sprintf("unknown(%d)", p)
	}
}