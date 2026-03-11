package redis

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	goredis "github.com/redis/go-redis/v9"
	"spread_kushat_pora_golang/internal/domain/entity"
)

const (
	snapshotKey     = "spread:snapshot"
	historyLastSeen = "spread:history:lastseen"
	historyPrefix   = "spread:history:"
	pricePrefix     = "spread:price:"
)

type StateRepository struct {
	client *goredis.Client
}

func NewStateRepository(client *goredis.Client) *StateRepository {
	return &StateRepository{client: client}
}

func (r *StateRepository) SaveSnapshot(ctx context.Context, snapshot entity.WatcherSnapshot) error {
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	return r.client.Set(ctx, snapshotKey, payload, 0).Err()
}

func (r *StateRepository) LoadSnapshot(ctx context.Context) (entity.WatcherSnapshot, error) {
	raw, err := r.client.Get(ctx, snapshotKey).Bytes()
	if err == goredis.Nil {
		return entity.WatcherSnapshot{}, nil
	}
	if err != nil {
		return entity.WatcherSnapshot{}, err
	}
	var snap entity.WatcherSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return entity.WatcherSnapshot{}, err
	}
	return snap, nil
}

func (r *StateRepository) AppendSpreadHistory(ctx context.Context, historyKey string, point entity.SpreadPoint, limit int) error {
	payload, err := json.Marshal(point)
	if err != nil {
		return err
	}
	redisKey := historyPrefix + historyKey
	pipe := r.client.TxPipeline()
	pipe.RPush(ctx, redisKey, payload)
	pipe.LTrim(ctx, redisKey, int64(-limit), -1)
	_, err = pipe.Exec(ctx)
	return err
}

func (r *StateRepository) GetSpreadHistory(ctx context.Context, historyKey string) ([]entity.SpreadPoint, error) {
	rows, err := r.client.LRange(ctx, historyPrefix+historyKey, 0, -1).Result()
	if err == goredis.Nil {
		return []entity.SpreadPoint{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]entity.SpreadPoint, 0, len(rows))
	for _, row := range rows {
		var p entity.SpreadPoint
		if json.Unmarshal([]byte(row), &p) == nil {
			out = append(out, p)
		}
	}
	return out, nil
}

func (r *StateRepository) SetHistoryLastSeen(ctx context.Context, historyKey string, ts int64) error {
	return r.client.HSet(ctx, historyLastSeen, historyKey, ts).Err()
}

func (r *StateRepository) GetHistoryLastSeen(ctx context.Context) (map[string]int64, error) {
	values, err := r.client.HGetAll(ctx, historyLastSeen).Result()
	if err == goredis.Nil {
		return map[string]int64{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(values))
	for k, v := range values {
		n, convErr := strconv.ParseInt(v, 10, 64)
		if convErr != nil {
			continue
		}
		out[k] = n
	}
	return out, nil
}

func (r *StateRepository) DeleteSpreadHistory(ctx context.Context, historyKey string) error {
	pipe := r.client.TxPipeline()
	pipe.Del(ctx, historyPrefix+historyKey)
	pipe.HDel(ctx, historyLastSeen, historyKey)
	_, err := pipe.Exec(ctx)
	return err
}

func (r *StateRepository) AppendPriceHistory(ctx context.Context, key string, point entity.PricePoint, limit int) error {
	payload, err := json.Marshal(point)
	if err != nil {
		return err
	}
	redisKey := pricePrefix + key
	pipe := r.client.TxPipeline()
	pipe.RPush(ctx, redisKey, payload)
	pipe.LTrim(ctx, redisKey, int64(-limit), -1)
	_, err = pipe.Exec(ctx)
	return err
}

func (r *StateRepository) GetPriceHistoryByPair(ctx context.Context, pair string) (map[string][]entity.PricePoint, error) {
	cursor := uint64(0)
	pattern := pricePrefix + pair + "|*"
	out := map[string][]entity.PricePoint{}

	for {
		keys, nextCursor, err := r.client.Scan(ctx, cursor, pattern, 200).Result()
		if err != nil {
			return nil, err
		}
		for _, key := range keys {
			rows, err := r.client.LRange(ctx, key, 0, -1).Result()
			if err != nil {
				continue
			}
			points := make([]entity.PricePoint, 0, len(rows))
			for _, row := range rows {
				var p entity.PricePoint
				if json.Unmarshal([]byte(row), &p) == nil {
					points = append(points, p)
				}
			}
			logicalKey := strings.TrimPrefix(key, pricePrefix)
			out[logicalKey] = points
		}
		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}

	return out, nil
}
