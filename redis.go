package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/garyburd/redigo/redis"
	"github.com/spf13/viper"
)

// name:barcode = name of product from api
// rating:barcode:users = userid[one] userid[two] userid[five]
// rating:barcode = one[count users] two[count users] three[count users] four[count users] five[count users]

var (
	pool *redis.Pool
)

func init() {
	redisHost := viper.GetString("REDIS_HOST")
	if redisHost == "" {
		redisHost = ":6379"
	}
	pool = newPool(redisHost)
	cleanupHook()
}

func newPool(server string) *redis.Pool {
	return &redis.Pool{
		MaxIdle:     10,
		IdleTimeout: 240 * time.Second,

		Dial: func() (redis.Conn, error) {
			c, err := redis.Dial("tcp", server)
			if err != nil {
				return nil, err
			}
			return c, err
		},

		TestOnBorrow: func(c redis.Conn, t time.Time) error {
			_, err := c.Do("PING")
			return err
		},
	}
}

func cleanupHook() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	signal.Notify(c, syscall.SIGTERM)
	signal.Notify(c, syscall.SIGKILL)
	go func() {
		<-c
		pool.Close()
		os.Exit(0)
	}()
}

func getBarcodeName(key string) ([]byte, error) {
	conn := pool.Get()
	defer conn.Close()

	var data []byte
	data, err := redis.Bytes(conn.Do("GET", key))
	if err != nil {
		return data, fmt.Errorf("error getting key %s: %v", key, err)
	}
	return data, err
}

func setBarcodeName(key string, value []byte) error {
	conn := pool.Get()
	defer conn.Close()

	_, err := conn.Do("SET", key, value)
	if err != nil {
		v := string(value)
		if len(v) > 15 {
			v = v[0:12] + "..."
		}
		return fmt.Errorf("error setting key %s to %s: %v", key, v, err)
	}
	return err
}

func getBarcodeRaiting(key string) (raiting, error) {
	conn := pool.Get()
	defer conn.Close()

	var r raiting
	data, err := redis.Values(conn.Do("HGETALL", key))
	if err != nil {
		return r, fmt.Errorf("error getting key %s: %v", key, err)
	}
	if redis.ScanStruct(data, &r); err != nil {
		log.Printf("error ScanStruct: %s", err)
	}
	return r, err
}

func incBarcodeRaiting(key, field string, value []byte) error {
	conn := pool.Get()
	defer conn.Close()

	chk, err := redis.Bool(conn.Do("HEXISTS", key, field))
	if err != nil && err != redis.ErrNil {
		return fmt.Errorf("HEXISTS error for %v: %s", field, err)
	}
	if !chk {
		err := setBarcodeRaiting(key, field, []byte("1"))
		if err != nil {
			return fmt.Errorf("incBarcodeRaiting: error setBarcodeRaiting for key %v value %v: %s", key, field, err)
		}
	} else {
		_, err := redis.Int(conn.Do("HINCRBY", key, field, value))
		if err != nil {
			return fmt.Errorf("HINCRBY error for %v: %s", field, err)
		}
	}
	return nil
}

func setBarcodeRaiting(key, field string, value []byte) error {
	conn := pool.Get()
	defer conn.Close()

	_, err := redis.Bool(conn.Do("HSET", key, field, value))
	if err != nil && err != redis.ErrNil {
		return fmt.Errorf("setBarcodeRaiting: error setBarcodeRaiting for key %v value %v: %s", key, field, err)
	}
	return nil
}

func getBarcodeRaitingUser(key, field string) ([]byte, error) {
	conn := pool.Get()
	defer conn.Close()

	var data []byte
	data, err := redis.Bytes(conn.Do("HGET", key, field))
	if err != nil {
		return data, fmt.Errorf("getBarcodeRaitingUser: error getBarcodeRaitingUser for key %v field %v: %s", key, field, err)
	}
	return data, err
}

func getBarcodeRaitingLen(key string) (int64, error) {
	conn := pool.Get()
	defer conn.Close()

	data, err := redis.Int64(conn.Do("HLEN", key))
	if err != nil {
		return data, fmt.Errorf("getBarcodeRaitingLen: error HLEN for key %v: %s", key, err)
	}
	return data, err
}
