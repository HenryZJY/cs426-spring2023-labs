package kv

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	kvpb "cs426.yale.edu/lab4/kv/proto"
	"github.com/sirupsen/logrus"
)

type Kv struct {
	shardMap   *ShardMap
	clientPool ClientPool

	// Add any client-side state you want here
	LBCounter uint32 // Round-Robin Load Balancer
	lock      sync.RWMutex
}

func MakeKv(shardMap *ShardMap, clientPool ClientPool) *Kv {
	kv := &Kv{
		shardMap:   shardMap,
		clientPool: clientPool,
	}
	// Add any initialization logic
	return kv
}

func (kv *Kv) get(ctx context.Context, key string, node string) (string, bool, error) {
	kv.lock.RLock()
	defer kv.lock.RUnlock()
	defer atomic.AddUint32(&kv.LBCounter, 1)
	kvClient, err := kv.clientPool.GetClient(node)
	if err != nil {
		return "", false, err
	}
	res, err := kvClient.Get(ctx, &kvpb.GetRequest{Key: key})
	if err != nil {
		return "", false, err
	}
	return res.GetValue(), res.GetWasFound(), nil
}

func (kv *Kv) Get(ctx context.Context, key string) (string, bool, error) {
	// Trace-level logging -- you can remove or use to help debug in your tests
	// with `-log-level=trace`. See the logging section of the spec.
	logrus.WithFields(
		logrus.Fields{"key": key},
	).Trace("client sending Get() request")

	shard := GetShardForKey(key, 1)
	nodes := kv.shardMap.NodesForShard(shard)
	var err error
	for i := 0; i < len(nodes); i++ {
		value, wasFound, e := kv.get(ctx, key, nodes[int(kv.LBCounter)%len(nodes)])
		if e == nil {
			return value, wasFound, nil
		}
		err = e
	}
	return "", false, err
}

func (kv *Kv) set(ctx context.Context, key string, value string, node string) error {
	kv.lock.Lock()
	defer kv.lock.Unlock()
	kvClient, err := kv.clientPool.GetClient(node)
	if err != nil {
		return err
	}
	_, err = kvClient.Set(ctx, &kvpb.SetRequest{Key: key, Value: value})
	return err
}

func (kv *Kv) Set(ctx context.Context, key string, value string, ttl time.Duration) error {
	logrus.WithFields(
		logrus.Fields{"key": key},
	).Trace("client sending Set() request")

	shard := GetShardForKey(key, 1)
	nodes := kv.shardMap.NodesForShard(shard)

	err_ch := make(chan error)

	for i := 0; i < len(nodes); i++ {
		go func(node string) {
			err_ch <- kv.set(ctx, key, value, node)
		}(nodes[i])
	}

	var err error
	for i := 0; i < len(nodes); i++ {
		if e := <-err_ch; e != nil {
			err = e
		}
	}
	close(err_ch)
	return err
}

func (kv *Kv) delete(ctx context.Context, key string, node string) error {
	kv.lock.Lock()
	defer kv.lock.Unlock()
	kvClient, err := kv.clientPool.GetClient(node)
	if err != nil {
		return err
	}
	_, err = kvClient.Delete(ctx, &kvpb.DeleteRequest{Key: key})
	return err
}

func (kv *Kv) Delete(ctx context.Context, key string) error {
	logrus.WithFields(
		logrus.Fields{"key": key},
	).Trace("client sending Delete() request")

	shard := GetShardForKey(key, 1)
	nodes := kv.shardMap.NodesForShard(shard)

	err_ch := make(chan error)

	for i := 0; i < len(nodes); i++ {
		go func(node string) {
			err_ch <- kv.delete(ctx, key, node)
		}(nodes[i])
	}

	var err error
	for i := 0; i < len(nodes); i++ {
		if e := <-err_ch; e != nil {
			err = e
		}
	}
	close(err_ch)
	return err
}
