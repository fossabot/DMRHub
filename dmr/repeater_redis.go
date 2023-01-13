package dmr

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/USA-RedDragon/dmrserver-in-a-box/models"
	"github.com/go-redis/redis"
	"k8s.io/klog/v2"
)

type redisRepeaterStorage struct {
	Redis *redis.Client
}

func makeRedisRepeaterStorage(redisAddr string) redisRepeaterStorage {
	return redisRepeaterStorage{
		Redis: redis.NewClient(&redis.Options{
			Addr: redisAddr,
		}),
	}
}

func (s redisRepeaterStorage) ping(repeaterID uint) {
	repeater := s.get(repeaterID)
	repeater.LastPing = time.Now()
	s.store(repeaterID, repeater)
	s.Redis.Expire(fmt.Sprintf("repeater:%d", repeaterID), 5*time.Minute)
}

func (s redisRepeaterStorage) updateConnection(repeaterID uint, connection string) {
	repeater := s.get(repeaterID)
	repeater.Connection = connection
	s.store(repeaterID, repeater)
}

func (s redisRepeaterStorage) delete(repeaterId uint) bool {
	return s.Redis.Del(fmt.Sprintf("repeater:%d", repeaterId)).Val() == 1
}

func (s redisRepeaterStorage) store(repeaterId uint, repeater models.Repeater) {
	repeaterBytes, err := repeater.MarshalMsg(nil)
	if err != nil {
		klog.Errorf("Error marshalling repeater", err)
		return
	}
	// Expire repeaters after 5 minutes, this function called often enough to keep them alive
	s.Redis.Set(fmt.Sprintf("repeater:%d", repeaterId), repeaterBytes, 5*time.Minute)
}

func (s redisRepeaterStorage) get(repeaterId uint) models.Repeater {
	repeaterBits, err := s.Redis.Get(fmt.Sprintf("repeater:%d", repeaterId)).Result()
	if err != nil {
		klog.Errorf("Error getting repeater from redis", err)
	}
	var repeater models.Repeater
	_, err = repeater.UnmarshalMsg([]byte(repeaterBits))
	if err != nil {
		klog.Errorf("Error unmarshalling repeater", err)
		return models.Repeater{}
	}
	return repeater
}

func (s redisRepeaterStorage) exists(repeaterId uint) bool {
	return s.Redis.Exists(fmt.Sprintf("repeater:%d", repeaterId)).Val() == 1
}

func (s redisRepeaterStorage) list() ([]uint, error) {
	var cursor uint64
	var repeaters []uint
	for {
		keys, cursor, err := s.Redis.Scan(cursor, "repeater:*", 0).Result()
		if err != nil {
			return nil, err
		}
		for _, key := range keys {
			repeaterNum, err := strconv.Atoi(strings.Replace(key, "repeater:", "", 1))
			if err != nil {
				return nil, err
			}
			repeaters = append(repeaters, uint(repeaterNum))
		}

		if cursor == 0 {
			break
		}
	}
	return repeaters, nil
}
