package device

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"smartwatch/internal/ble"
	"smartwatch/internal/config"
)

type fakeSessionLink struct {
	gen    uint64
	notifs chan ble.Notification
	writes chan fakeWrite
}

type fakeWrite struct {
	protocol ble.Protocol
	packet   []byte
}

func newFakeSessionLink() *fakeSessionLink {
	return &fakeSessionLink{
		gen:    1,
		notifs: make(chan ble.Notification, 32),
		writes: make(chan fakeWrite, 32),
	}
}

func (l *fakeSessionLink) Write(_ context.Context, p ble.Protocol, packet []byte) error {
	cp := bytes.Clone(packet)
	l.writes <- fakeWrite{protocol: p, packet: cp}
	return nil
}

func (l *fakeSessionLink) Notifications() <-chan ble.Notification { return l.notifs }
func (l *fakeSessionLink) Gen() uint64                            { return l.gen }

func waitForWrite(t *testing.T, l *fakeSessionLink, want []byte) {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		select {
		case w := <-l.writes:
			if bytes.Equal(w.packet, want) {
				return
			}
		case <-deadline:
			t.Fatalf("timed out waiting for write % X", want)
		}
	}
}

func waitForNotificationsDrained(t *testing.T, l *fakeSessionLink) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(l.notifs) == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for notification drain")
}

func TestCancelledTransactionIsClearedBeforeLatePacketAndNextSameCommand(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := New(nil, logger)
	link := newFakeSessionLink()

	sessionCtx, cancelSession := context.WithCancel(context.Background())
	defer cancelSession()

	sessionDone := make(chan error, 1)
	go func() {
		sessionDone <- m.runSession(sessionCtx, link, config.Config{PairedAddr: "addr", PairedName: "watch"})
	}()
	defer func() {
		cancelSession()
		select {
		case <-sessionDone:
		case <-time.After(time.Second):
			t.Fatal("session did not stop")
		}
	}()

	// First request times out without receiving a terminator.
	aCtx, cancelA := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelA()
	aFeedCalled := make(chan struct{}, 1)
	aDone := make(chan error, 1)
	aPacket := []byte{0x15, 0xAA}
	go func() {
		_, err := m.do(aCtx, ble.V1, 0x15, aPacket, func([]byte) (bool, any, error) {
			select {
			case aFeedCalled <- struct{}{}:
			default:
			}
			return true, nil, nil
		})
		aDone <- err
	}()
	waitForWrite(t, link, aPacket)

	if err := <-aDone; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("first txn err = %v, want deadline", err)
	}

	// A late packet for the cancelled command must not be fed to the cancelled
	// transaction; it should be treated as unsolicited once ctx is done.
	link.notifs <- ble.Notification{Gen: link.gen, Protocol: ble.V1, Data: []byte{0x15, 0x01}}
	waitForNotificationsDrained(t, link)
	select {
	case <-aFeedCalled:
		t.Fatal("late packet was delivered to cancelled transaction")
	default:
	}

	// A second request with the same cmd should be accepted and complete cleanly.
	bCtx, cancelB := context.WithTimeout(context.Background(), time.Second)
	defer cancelB()
	bDone := make(chan struct {
		data any
		err  error
	}, 1)
	bPacket := []byte{0x15, 0xBB}
	go func() {
		data, err := m.do(bCtx, ble.V1, 0x15, bPacket, func(pkt []byte) (bool, any, error) {
			return true, bytes.Clone(pkt), nil
		})
		bDone <- struct {
			data any
			err  error
		}{data: data, err: err}
	}()
	waitForWrite(t, link, bPacket)

	wantResp := []byte{0x15, 0x02}
	link.notifs <- ble.Notification{Gen: link.gen, Protocol: ble.V1, Data: wantResp}

	select {
	case r := <-bDone:
		if r.err != nil {
			t.Fatalf("second txn err = %v", r.err)
		}
		got, ok := r.data.([]byte)
		if !ok || !bytes.Equal(got, wantResp) {
			t.Fatalf("second txn data = %#v, want % X", r.data, wantResp)
		}
	case <-time.After(time.Second):
		t.Fatal("second txn did not complete")
	}
}
