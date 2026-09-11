// Package events：NATS JetStream 事件总线（download.plan/shard/retry/completed、dataset.ready 等主题的轻量消息通道）。
// 大数据不进 NATS —— 队列只传 job_id / shard_id / metadata，数据本体在 MinIO / ClickHouse。
package events

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"
)

var ErrDisabled = errors.New("event bus disabled")

type Bus struct {
	nc      *nats.Conn
	js      nats.JetStreamContext
	log     *slog.Logger
	enabled bool
}

func New(url string, log *slog.Logger) *Bus {
	b := &Bus{log: log}
	if url == "" {
		return b
	}
	nc, err := nats.Connect(url,
		nats.Timeout(3*time.Second),
		nats.MaxReconnects(3),
		nats.ReconnectWait(time.Second),
	)
	if err != nil {
		log.Warn("nats unavailable, event bus disabled", "err", err)
		return b
	}
	js, err := nc.JetStream()
	if err != nil {
		log.Warn("nats jetstream unavailable", "err", err)
		nc.Close()
		return b
	}
	_, err = js.AddStream(&nats.StreamConfig{
		Name:     "EVENTS",
		Subjects: []string{"events.>"},
		Storage:  nats.FileStorage,
		MaxAge:   24 * time.Hour,
	})
	if err != nil {
		log.Warn("nats add stream failed", "err", err)
	}
	b.nc = nc
	b.js = js
	b.enabled = true
	return b
}

func (b *Bus) Enabled() bool { return b != nil && b.enabled }

func (b *Bus) Publish(ctx context.Context, subject string, payload any) {
	if !b.Enabled() {
		return
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	if err := b.PublishRaw(ctx, subject, data, ""); err != nil {
		b.log.Warn("nats publish failed", "subject", subject, "err", err)
	}
}

// PublishRaw publishes an already encoded envelope. eventID is forwarded as
// Nats-Msg-Id so JetStream deduplicates outbox retries.
func (b *Bus) PublishRaw(ctx context.Context, subject string, data []byte, eventID string) error {
	if !b.Enabled() {
		return ErrDisabled
	}
	msg := &nats.Msg{Subject: subject, Data: data, Header: nats.Header{}}
	if eventID != "" {
		msg.Header.Set(nats.MsgIdHdr, eventID)
	}
	_, err := b.js.PublishMsg(msg, nats.Context(ctx))
	return err
}

// Subscribe 订阅主题（DeliverNew：只接收新事件）。回调携带消息主题。
func (b *Bus) Subscribe(subject string, cb func(subject string, data []byte)) (func(), error) {
	if !b.Enabled() {
		return nil, ErrDisabled
	}
	sub, err := b.js.Subscribe(subject, func(m *nats.Msg) { cb(m.Subject, m.Data) }, nats.DeliverNew())
	if err != nil {
		return nil, err
	}
	return func() { _ = sub.Unsubscribe() }, nil
}

func (b *Bus) Close() {
	if b.nc != nil {
		b.nc.Close()
	}
}
