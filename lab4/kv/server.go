package kv

import (
	"context"
	"log"
	"math/rand"
	"sync"
	"time"

	"cs426.yale.edu/lab4/kv/proto"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type kvObject struct {
	value       string
	stored_time time.Time
	ttlMs       int64
}

type KvServerImpl struct {
	proto.UnimplementedKvServer
	nodeName string

	shardMap   *ShardMap
	listener   *ShardMapListener
	clientPool ClientPool
	shutdown   chan struct{}

	storage     map[string]*kvObject
	mu          sync.RWMutex
	ttlShutdown chan bool

	shards map[int32]*kvShard
}

type kvShard struct {
	data   map[string]*kvObject
	mu     sync.RWMutex
	shutCh chan bool
}

func MakeKvShard() *kvShard {
	shard := kvShard{data: make(map[string]*kvObject), shutCh: make(chan bool)}
	go shard.ttlMonitor()
	return &shard
}

func (shard *kvShard) Shutdown() {
	shard.shutCh <- true
}

func (shard *kvShard) cleanup() {
	shard.mu.Lock()
	defer shard.mu.Unlock()
	var expired []string
	for key, obj := range shard.data {
		if hasExpired(obj) {
			expired = append(expired, key)
		}
	}
	for _, key := range expired {
		delete(shard.data, key)
	}
}

func (shard *kvShard) ttlMonitor() {
	for {
		select {
		case <-shard.shutCh:
			return
		case <-time.After(time.Second * 1):
			shard.cleanup()
		}
	}
}

func (shard *kvShard) Get(key string) (*kvObject, bool) {
	shard.mu.RLock()
	defer shard.mu.RUnlock()
	obj, ok := shard.data[key]
	if !ok || hasExpired(obj) {
		return nil, false
	}
	return obj, ok
}

func (shard *kvShard) Set(key string, value string, ttlMs int64) {
	shard.mu.Lock()
	defer shard.mu.Unlock()
	shard.data[key] = &kvObject{
		value:       value,
		stored_time: time.Now(),
		ttlMs:       ttlMs,
	}
}

func (shard *kvShard) Delete(key string) {
	shard.mu.Lock()
	defer shard.mu.Unlock()
	delete(shard.data, key)
}

func (shard *kvShard) Data() map[string]*kvObject {
	shard.mu.RLock()
	defer shard.mu.RUnlock()
	return shard.data
}

func (server *KvServerImpl) fetchShards() map[int32]bool {
	shards := make(map[int32]bool)
	for _, shard := range server.shardMap.ShardsForNode(server.nodeName) {
		shards[int32(shard)] = true
	}
	return shards
}

func (server *KvServerImpl) fetchNodeShardContent(node string, shard int32) (*proto.GetShardContentsResponse, error) {
	client, err := server.clientPool.GetClient(node)
	if err != nil {
		return nil, err
	}
	// fmt.Println(server.nodeName, node, shard)
	res, err := client.GetShardContents(context.Background(), &proto.GetShardContentsRequest{
		Shard: shard,
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

func (server *KvServerImpl) fetchShardContent(nodes []string, shard int32) (*proto.GetShardContentsResponse, error) {
	var err error = nil
	index := rand.Intn(len(nodes))
	for i := 0; i < len(nodes); i++ {
		node := nodes[(index+i)%len(nodes)]
		if node == server.nodeName {
			continue
		}
		res, e := server.fetchNodeShardContent(node, shard)
		if e == nil {
			return res, nil
		}
		err = e
	}
	log.Println("shard not available")
	return &proto.GetShardContentsResponse{}, err
}

// func (server *KvServerImpl) deleteShards(shards map[int32]bool) {
// 	// if len(shards) == 0 {
// 	// 	return
// 	// }
// 	// for key := range server.storage {
// 	// 	shard := GetShardForKey(key, server.shardMap.NumShards())
// 	// 	if _, exists := shards[int32(shard)]; exists {
// 	// 		server.storage[key].ttlMs = 0 // make expired
// 	// 	}
// 	// }

// 	// for shard := range shards {
// 	// 	delete(server.shards, shard)
// 	// }
// 	for shard := range shards {
// 		server.shards[shard].Shutdown()
// 		delete(server.shards, shard)
// 	}
// }

func (server *KvServerImpl) handleShardMapUpdate() {
	server.mu.Lock()
	defer server.mu.Unlock()

	shards := server.fetchShards()
	added := make(map[int32]bool)   // new shards
	removed := make(map[int32]bool) // shards to be removed

	for shard := range shards {
		if _, exists := server.shards[shard]; !exists {
			added[shard] = true
		}
	}

	for shard := range server.shards {
		if _, exists := shards[shard]; !exists {
			removed[shard] = true
		}
	}

	for shard := range removed {
		server.shards[shard].Shutdown()
		delete(server.shards, shard)
	}

	if len(added) == 0 {
		return
	}
	resCh := make(chan *proto.GetShardContentsResponse, len(added))
	for shard := range added {
		server.shards[shard] = MakeKvShard()
		go func(shard int32, nodes []string) {
			res, _ := server.fetchShardContent(nodes, shard)
			resCh <- res
		}(shard, server.shardMap.NodesForShard(int(shard)))
	}
	server.mu.Unlock()
	results := make([]*proto.GetShardContentsResponse, 0)
	for i := 0; i < len(added); i++ {
		results = append(results, <-resCh)
	}
	close(resCh)
	server.mu.Lock()
	for _, res := range results {
		for _, shardVal := range res.Values {
			shard := GetShardForKey(shardVal.Key, server.shardMap.NumShards())
			server.shards[int32(shard)].Set(shardVal.Key, shardVal.Value, shardVal.TtlMsRemaining)
		}
	}
}

func (server *KvServerImpl) shardMapListenLoop() {
	listener := server.listener.UpdateChannel()
	for {
		select {
		case <-server.shutdown:
			return
		case <-listener:
			server.handleShardMapUpdate()
		}
	}
}

func MakeKvServer(nodeName string, shardMap *ShardMap, clientPool ClientPool) *KvServerImpl {
	listener := shardMap.MakeListener()
	server := KvServerImpl{
		nodeName:   nodeName,
		shardMap:   shardMap,
		listener:   &listener,
		clientPool: clientPool,
		shutdown:   make(chan struct{}),
		// storage:     make(map[string]*kvObject),
		ttlShutdown: make(chan bool),
		shards:      make(map[int32]*kvShard),
	}
	// server.shards = server.fetchShards()
	go server.shardMapListenLoop()
	server.handleShardMapUpdate()
	// go server.ttlMonitor()
	return &server
}

func (server *KvServerImpl) Shutdown() {
	server.shutdown <- struct{}{}
	server.listener.Close()
	for _, shard := range server.shards {
		shard.Shutdown()
	}
	// server.ttlShutdown <- true
}

// func (server *KvServerImpl) cleanup() {
// 	server.mu.Lock()
// 	defer server.mu.Unlock()
// 	var expired []string
// 	for _, shard := range server.shards {
// 		for key, obj := range shard.Data() {
// 			if hasExpired(obj) {
// 				expired = append(expired, key)
// 			}
// 		}
// 	}
// 	// for key, obj := range server.storage {
// 	// 	if hasExpired(obj) || !server.isShardHosted(GetShardForKey(key, server.shardMap.NumShards())) {
// 	// 		expired = append(expired, key)
// 	// 	}
// 	// }
// 	for _, key := range expired {
// 		shard := GetShardForKey(key, server.shardMap.NumShards())
// 		server.shards[int32(shard)].Delete(key)
// 		// delete(server.storage, key)
// 	}
// }

// func (server *KvServerImpl) ttlMonitor() {
// 	for {
// 		select {
// 		case <-server.ttlShutdown:
// 			return
// 		case <-time.After(time.Second * 1):
// 			server.cleanup()
// 		}
// 	}
// }

func (server *KvServerImpl) isShardHosted(shard int) bool {
	_, exists := server.shards[int32(shard)]
	return exists
}

func hasExpired(obj *kvObject) bool {
	// if obj.ttlMs == 0 {
	// 	return true
	// }
	return time.Since(obj.stored_time).Milliseconds() >= obj.ttlMs
}

func (server *KvServerImpl) Get(
	ctx context.Context,
	request *proto.GetRequest,
) (*proto.GetResponse, error) {
	// Trace-level logging for node receiving this request (enable by running with -log-level=trace),
	// feel free to use Trace() or Debug() logging in your code to help debug tests later without
	// cluttering logs by default. See the logging section of the spec.
	logrus.WithFields(
		logrus.Fields{"node": server.nodeName, "key": request.Key},
	).Trace("node received Get() request")

	server.mu.RLock()
	defer server.mu.RUnlock()

	shard := GetShardForKey(request.Key, server.shardMap.NumShards())
	if !server.isShardHosted(shard) {
		return &proto.GetResponse{}, status.Error(codes.NotFound, "shard not hosted")
	}

	if request.Key == "" {
		return &proto.GetResponse{}, status.Error(codes.InvalidArgument, "key cannot be empty")
	}
	obj, exists := server.shards[int32(shard)].Get(request.Key)
	if !exists {
		return &proto.GetResponse{
			Value:    "",
			WasFound: false,
		}, nil
	}
	return &proto.GetResponse{
		Value:    obj.value,
		WasFound: true,
	}, nil
}

func (server *KvServerImpl) Set(
	ctx context.Context,
	request *proto.SetRequest,
) (*proto.SetResponse, error) {
	logrus.WithFields(
		logrus.Fields{"node": server.nodeName, "key": request.Key},
	).Trace("node received Set() request")

	server.mu.Lock()
	defer server.mu.Unlock()
	shard := GetShardForKey(request.Key, server.shardMap.NumShards())
	if !server.isShardHosted(shard) {
		return &proto.SetResponse{}, status.Error(codes.NotFound, "shard not hosted")
	}

	if request.Key == "" {
		return &proto.SetResponse{}, status.Error(codes.InvalidArgument, "key cannot be empty")
	}
	server.shards[int32(shard)].Set(request.Key, request.Value, request.TtlMs)
	return &proto.SetResponse{}, nil
}

func (server *KvServerImpl) Delete(
	ctx context.Context,
	request *proto.DeleteRequest,
) (*proto.DeleteResponse, error) {
	logrus.WithFields(
		logrus.Fields{"node": server.nodeName, "key": request.Key},
	).Trace("node received Delete() request")

	server.mu.Lock()
	defer server.mu.Unlock()
	shard := GetShardForKey(request.Key, server.shardMap.NumShards())
	if !server.isShardHosted(shard) {
		return &proto.DeleteResponse{}, status.Error(codes.NotFound, "shard not hosted")
	}

	if request.Key == "" {
		return &proto.DeleteResponse{}, status.Error(codes.InvalidArgument, "key cannot be empty")
	}
	server.shards[int32(shard)].Delete(request.Key)
	return &proto.DeleteResponse{}, nil
}

func (server *KvServerImpl) GetShardContents(
	ctx context.Context,
	request *proto.GetShardContentsRequest,
) (*proto.GetShardContentsResponse, error) {

	server.mu.RLock()
	defer server.mu.RUnlock()
	if !server.isShardHosted(int(request.Shard)) {
		return &proto.GetShardContentsResponse{}, status.Error(codes.NotFound, "shard not hosted")
	}

	res := &proto.GetShardContentsResponse{
		Values: make([]*proto.GetShardValue, 0),
	}

	for key, obj := range server.shards[request.Shard].Data() {
		if !hasExpired(obj) {
			res.Values = append(res.Values, &proto.GetShardValue{
				Key:            key,
				Value:          obj.value,
				TtlMsRemaining: obj.ttlMs - time.Since(obj.stored_time).Milliseconds(),
			})
		}
	}

	return res, nil
}
