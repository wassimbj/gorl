package main

import (
	"context"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
)

type RLOpts struct {
	Attempts    int
	Prefix      string
	Duration    time.Duration
	Id          string // user identifier
	RedisConfig redis.Options
}

func getRedis(opts redis.Options) (*redis.Client, error) {

	rdb := redis.NewClient(&opts)

	if rdb.Ping(context.Background()).Err() != nil {
		return nil, rdb.Ping(context.Background()).Err()
	}

	return rdb, nil
}

type RLResult struct {
	AttemptsLeft int
	Used         int   // used attempts
	TimeLeft     int64 // in ms, time left until the bucket gets refilled
	Block        bool  // should the user get blocked
}

func RateLimiter(ctx context.Context, opts RLOpts) (RLResult, error) {
	rds, err := getRedis(opts.RedisConfig)

	if err != nil {
		// nohting
		return RLResult{
			AttemptsLeft: 0,
			Used:         0,
			TimeLeft:     0,
			Block:        false,
		}, err
	}

	// construct the key "gorl:{prefix}:{id}", prefix ∈ (login, signup...) && id is a unique one can be an IP, userId...
	key := fmt.Sprintf("gorl:%s:%s", opts.Prefix, opts.Id)

	lock := NewLock(rds)
	lockId := lock.Acquire(ctx, opts.Prefix, time.Second*2)
	defer lock.Release(ctx, opts.Prefix, lockId)

	data := rds.Get(ctx, key)

	attemptsLeft, _ := data.Int()
	timeLeft := rds.PTTL(ctx, key).Val()

	// no data found, either the attempts expired or its the first time this user is making the request.
	if attemptsLeft <= 0 && timeLeft < 0 {
		setResult := rds.Set(ctx, key, opts.Attempts-1, opts.Duration)

		if setResult.Err() != nil {
			// log.Fatalf("INIT ERROR %s", setResult.Err().Error())
			return RLResult{}, setResult.Err()
		}
		attemptsLeft = opts.Attempts - 1
		return RLResult{
			AttemptsLeft: attemptsLeft,
			Used:         1,
			TimeLeft:     opts.Duration.Milliseconds(),
			Block:        false,
		}, nil
		// allow
	} else {
		if attemptsLeft <= 0 {
			// block user
			return RLResult{
				AttemptsLeft: 0,
				Used:         opts.Attempts,
				TimeLeft:     timeLeft.Milliseconds(),
				Block:        true,
			}, nil
		} else {
			// update the attempts left
			decrCmd := rds.Decr(ctx, key)

			attemptsLeft = int(decrCmd.Val())

			// allow the user
			return RLResult{
				AttemptsLeft: attemptsLeft,
				Used:         opts.Attempts - attemptsLeft,
				TimeLeft:     timeLeft.Milliseconds(),
				Block:        false,
			}, nil
		}
	}

}
