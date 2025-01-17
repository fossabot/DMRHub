package dmr

import (
	"context"

	"github.com/USA-RedDragon/DMRHub/internal/models"
	"github.com/redis/go-redis/v9"
	"k8s.io/klog/v2"
)

// Parrot is a struct that stores packets and repeats them back to the repeater
type Parrot struct {
	Redis redisParrotStorage
}

// NewParrot creates a new parrot instance
func NewParrot(redis *redis.Client) *Parrot {
	return &Parrot{
		Redis: makeRedisParrotStorage(redis),
	}
}

// IsStarted returns true if the stream is already started
func (p *Parrot) IsStarted(ctx context.Context, streamID uint) bool {
	return p.Redis.exists(ctx, streamID)
}

// StartStream starts a new stream
func (p *Parrot) StartStream(ctx context.Context, streamID uint, repeaterID uint) bool {
	if !p.Redis.exists(ctx, streamID) {
		p.Redis.store(ctx, streamID, repeaterID)
		return true
	}
	klog.Warningf("Parrot: Stream %d already started", streamID)
	return false
}

// RecordPacket records a packet from the stream
func (p *Parrot) RecordPacket(ctx context.Context, streamID uint, packet models.Packet) {
	go p.Redis.refresh(ctx, streamID)

	// Grab the repeater ID to go ahead and mark the packet as being routed back
	repeaterID, err := p.Redis.get(ctx, streamID)
	if err != nil {
		klog.Errorf("Error getting parrot stream from redis", err)
		return
	}

	packet.Repeater = repeaterID
	tmpSrc := packet.Src
	packet.Src = packet.Dst
	packet.Dst = tmpSrc
	packet.GroupCall = false
	packet.BER = -1
	packet.RSSI = -1

	err = p.Redis.stream(ctx, streamID, packet)
	if err != nil {
		klog.Errorf("Error storing parrot stream in redis", err)
	}
}

// StopStream stops a stream
func (p *Parrot) StopStream(ctx context.Context, streamID uint) {
	p.Redis.delete(ctx, streamID)
}

// GetStream returns the stream
func (p *Parrot) GetStream(ctx context.Context, streamID uint) []models.Packet {
	// Empty array of packet byte arrays
	packets, err := p.Redis.getStream(ctx, streamID)
	if err != nil {
		klog.Errorf("Error getting parrot stream from redis: %s", err)
		return nil
	}

	return packets
}
