package dmr

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/USA-RedDragon/DMRHub/internal/models"
	"github.com/redis/go-redis/v9"
)

type redisParrotStorage struct {
	Redis *redis.Client
}

func makeRedisParrotStorage(redis *redis.Client) redisParrotStorage {
	return redisParrotStorage{
		Redis: redis,
	}
}

func (r *redisParrotStorage) store(ctx context.Context, streamID uint, repeaterID uint) {
	r.Redis.Set(ctx, fmt.Sprintf("parrot:stream:%d", streamID), repeaterID, 5*time.Minute)
}

func (r *redisParrotStorage) exists(ctx context.Context, streamID uint) bool {
	return r.Redis.Exists(ctx, fmt.Sprintf("parrot:stream:%d", streamID)).Val() == 1
}

func (r *redisParrotStorage) refresh(ctx context.Context, streamID uint) {
	r.Redis.Expire(ctx, fmt.Sprintf("parrot:stream:%d", streamID), 5*time.Minute)
}

func (r *redisParrotStorage) get(ctx context.Context, streamID uint) (uint, error) {
	repeaterIDStr, err := r.Redis.Get(ctx, fmt.Sprintf("parrot:stream:%d", streamID)).Result()
	if err != nil {
		return 0, err
	}
	repeaterID, err := strconv.Atoi(repeaterIDStr)
	if err != nil {
		return 0, err
	}
	return uint(repeaterID), nil
}

func (r *redisParrotStorage) stream(ctx context.Context, streamID uint, packet models.Packet) error {
	packetBytes, err := packet.MarshalMsg(nil)
	if err != nil {
		return err
	}

	r.Redis.RPush(ctx, fmt.Sprintf("parrot:stream:%d:packets", streamID), packetBytes)
	return nil
}

func (r *redisParrotStorage) delete(ctx context.Context, streamID uint) {
	r.Redis.Del(ctx, fmt.Sprintf("parrot:stream:%d", streamID))
	r.Redis.Expire(ctx, fmt.Sprintf("parrot:stream:%d:packets", streamID), 5*time.Minute)
}

func (r *redisParrotStorage) getStream(ctx context.Context, streamID uint) ([]models.Packet, error) {
	// Empty array of packet byte arrays
	var packets [][]byte
	packetSize, err := r.Redis.LLen(ctx, fmt.Sprintf("parrot:stream:%d:packets", streamID)).Result()
	if err != nil {
		return nil, err
	}
	// Loop through the packets and add them to the array
	for i := int64(0); i < packetSize; i++ {
		packet, err := r.Redis.LIndex(ctx, fmt.Sprintf("parrot:stream:%d:packets", streamID), i).Bytes()
		if err != nil {
			return nil, err
		}
		packets = append(packets, packet)
	}
	// Delete the stream
	r.Redis.Del(ctx, fmt.Sprintf("parrot:stream:%d:packets", streamID))

	// Empty array of packets
	var packetArray []models.Packet
	// Loop through the packets and unmarshal them
	for _, packet := range packets {
		var packetObj models.Packet
		_, err := packetObj.UnmarshalMsg(packet)
		if err != nil {
			return nil, err
		}
		packetArray = append(packetArray, packetObj)
	}
	return packetArray, nil
}
