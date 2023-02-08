package server_lib

import (
	"context"
	"flag"
	"log"
	"sort"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	umc "cs426.yale.edu/lab1/user_service/mock_client"
	pb "cs426.yale.edu/lab1/video_rec_service/proto"
	vmc "cs426.yale.edu/lab1/video_service/mock_client"
	upb "cs426.yale.edu/lab1/user_service/proto"
	vpb "cs426.yale.edu/lab1/video_service/proto"
	ranker "cs426.yale.edu/lab1/ranker"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type VideoRecServiceOptions struct {
	// Server address for the UserService"
	UserServiceAddr string
	// Server address for the VideoService
	VideoServiceAddr string
	// Maximum size of batches sent to UserService and VideoService
	MaxBatchSize int
	// If set, disable fallback to cache
	DisableFallback bool
	// If set, disable all retries
	DisableRetry bool
}

type trendingVideos struct {
	videos []*vpb.VideoInfo
	mu sync.RWMutex
	expireTime int64
}

type VideoRecServiceServer struct {
	pb.UnimplementedVideoRecServiceServer
	options VideoRecServiceOptions
	// Add any data you want here
	mockUserFlag bool
	mockVideoFlag bool
	mockUserServiceClient *umc.MockUserServiceClient
	mockVideoServiceClient *vmc.MockVideoServiceClient
	numTopVideoRequests uint64
	numTopVideoErrs uint64
	numActiveRequests int64
	numUserServiceErrs uint64
	numVideoServiceErrs uint64
	totalLatency int64
	numStaleResponse uint64
	fallBackVideos *trendingVideos
}

type rankPair struct {
	vi *vpb.VideoInfo
	score uint64
}


func MakeVideoRecServiceServer(options VideoRecServiceOptions) (*VideoRecServiceServer, error) {
	res := &VideoRecServiceServer{
		options: options,
		// Add any data to initialize here
		mockUserFlag: false,
		mockVideoFlag: false,
		fallBackVideos: &trendingVideos{
			videos: nil,
		},
	}

	go fetchTrendingVideos(res)

	return res, nil
}

func MakeVideoRecServiceServerWithMocks(
	options VideoRecServiceOptions,
	mockUserServiceClient *umc.MockUserServiceClient,
	mockVideoServiceClient *vmc.MockVideoServiceClient,
) *VideoRecServiceServer {
	// TODO: Implement your own logic here
	
	res := &VideoRecServiceServer{
		options: options,
		mockUserFlag: true,
		mockVideoFlag: true,
		mockUserServiceClient: mockUserServiceClient,
		mockVideoServiceClient: mockVideoServiceClient,
		fallBackVideos: &trendingVideos{
			videos: nil,
		},
	}

	go fetchTrendingVideos(res)
	return res
}

func (server *VideoRecServiceServer) GetTopVideos(
	ctx context.Context,
	req *pb.GetTopVideosRequest,
) (*pb.GetTopVideosResponse, error) {
	// --------------------- First user request ----------------------
	start_time := time.Now().UnixMilli()
	atomic.AddInt64(&server.numActiveRequests, 1)
	defer atomic.AddInt64(&server.numActiveRequests, -1)
	defer atomic.AddUint64(&server.numTopVideoRequests, 1)
	user_ids := []uint64{req.GetUserId()}
	this_user_infos := make([]*upb.UserInfo, 0)
	user_request := &upb.GetUserRequest{UserIds: user_ids}
	video_candidates := make([]uint64, 0)
	var uclient upb.UserServiceClient

	if server.mockUserFlag {
		user_response, err := server.mockUserServiceClient.GetUser(ctx, user_request)
		if err != nil {
			atomic.AddUint64(&server.numUserServiceErrs, 1)
			// Fallback
			if server.options.DisableFallback == false {
				return fallBack(server, req)
			}
			atomic.AddUint64(&server.numTopVideoErrs, 1)
			log.Printf("fail to setup mock user service: %v", err)
			return nil, status.Error(
				codes.Unknown,
				"Mock User service client returned Error",
			)
		}
		this_user_infos = user_response.GetUsers()
		fmt.Println("Finished first user request for user", user_ids, this_user_infos[0].GetUserId())

	} else {
		fmt.Println("Starting first user request ")
		flag.Parse()
		var opts []grpc.DialOption
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
		uconn, err := grpc.Dial(server.options.UserServiceAddr, opts...)
		defer uconn.Close()
		if err != nil {
			// retry
			if server.options.DisableRetry == false {
				uconn, err = grpc.Dial(server.options.UserServiceAddr, opts...)
			}
			if err != nil {
				atomic.AddUint64(&server.numUserServiceErrs, 1)
				// Fallback
				if server.options.DisableFallback == false {
					return fallBack(server, req)
				}
				atomic.AddUint64(&server.numTopVideoErrs, 1)
				log.Printf("fail to dial: %v", err)
				return nil, err
			}
		}
		// TODO: Are there potential other error cases? Add and use different error codes
		uclient = upb.NewUserServiceClient(uconn)
		user_response, err := uclient.GetUser(ctx, user_request)
		this_user_infos = user_response.GetUsers()
		if err != nil {
			// fallback
			atomic.AddUint64(&server.numUserServiceErrs, 1)
			if server.options.DisableFallback == false {
				return fallBack(server, req)
			}
			atomic.AddUint64(&server.numTopVideoErrs, 1)
			log.Printf("fail to get userinfo 1: %v", err)
			return nil, err
		}
		fmt.Println("Finished first user request for user", user_ids, this_user_infos[0].GetUserId())
	}
	// ----------------------------- Second User Request --------------------------
	user_subscribe_to_ids := this_user_infos[0].GetSubscribedTo()
	
	fmt.Println("Starting second user request for the following users: ", user_subscribe_to_ids)
	offset := 0

	for offset < len(user_subscribe_to_ids) {
		upper := int(math.Min(float64(offset + server.options.MaxBatchSize), float64(len(user_subscribe_to_ids))))
		batch_subscribe_ids := user_subscribe_to_ids[offset:upper]
		fmt.Println(server.options.MaxBatchSize)
		second_user_request := &upb.GetUserRequest{UserIds: batch_subscribe_ids}

		var second_user_response *upb.GetUserResponse
		var err error
		if server.mockUserFlag {
			second_user_response, err = server.mockUserServiceClient.GetUser(ctx, second_user_request)

		} else {
			second_user_response, err = uclient.GetUser(ctx, second_user_request)
		}
		if err != nil {
			atomic.AddUint64(&server.numUserServiceErrs, 1)
			// Fallback
			if server.options.DisableFallback == false {
				return fallBack(server, req)
			}
			atomic.AddUint64(&server.numTopVideoErrs, 1)
			log.Printf("fail to get userinfo 2: %v", err)
			return nil, err
		}

		second_user_infos := second_user_response.GetUsers()
		batch_video_candidates := make([]uint64, 0)
		for _, info := range second_user_infos {
			batch_video_candidates = append(batch_video_candidates, info.GetLikedVideos()...)
		}
		offset += server.options.MaxBatchSize
		video_candidates = append(video_candidates, batch_video_candidates...)
	}
	

	// Deduplicate
	dedup_candidates := deduplicateIds(video_candidates)

	// ----------------------------- Video Request -----------------------------
	fmt.Println("Starting video requests for a number of ", len(dedup_candidates),  " video candidates: ", dedup_candidates)
	video_infos, err := fetchVideos(server, ctx, dedup_candidates)
	if err != nil {
		atomic.AddUint64(&server.numVideoServiceErrs, 1)
		// Fallback
		if server.options.DisableFallback == false {
			return fallBack(server, req)
		}
		atomic.AddUint64(&server.numTopVideoErrs, 1)
		log.Printf("fail to get video info: %v", err)
		return nil, err
	}
	// --------------------- Ranking ------------------------------------
	// fmt.Println("Starting to rank videos")
	my_ranker := ranker.BcryptRanker{}

	arr_to_rank := make([]rankPair, 0)
	u_coeff := this_user_infos[0].GetUserCoefficients()
	// fmt.Print("Now Ranking:  ")
	for _, video_info := range video_infos {
		// fmt.Print(video_info.GetVideoId(), "  ")
		v_coeff := video_info.GetVideoCoefficients()
		arr_to_rank = append(arr_to_rank, rankPair{video_info, my_ranker.Rank(u_coeff, v_coeff)})
	}
	// fmt.Println()

	sort.Slice(arr_to_rank, func(i,j int) bool {
		return arr_to_rank[i].score > arr_to_rank[j].score
	})

	sorted_video_infos := make([]*vpb.VideoInfo, 0)
	iter_len := len(arr_to_rank)
	if limit := req.GetLimit(); int(limit) < iter_len {
		iter_len = int(limit)
	}

	// fmt.Println("Recommending ", iter_len, " Videos, They are")
	for i := 0; i < iter_len; i++ {
		sorted_video_infos = append(sorted_video_infos, arr_to_rank[i].vi)
		// fmt.Println(arr_to_rank[i].vi.VideoId, arr_to_rank[i].vi.Title, arr_to_rank[i].vi.Author)
	}
	end_time := time.Now().UnixMilli()
	defer atomic.AddInt64(&server.totalLatency, end_time - start_time)

	return &pb.GetTopVideosResponse{Videos:sorted_video_infos}, nil
}

func fetchVideos(
	server *VideoRecServiceServer,
	ctx context.Context,
	video_candids []uint64,
) ([]*vpb.VideoInfo, error) {
	// Batch this
	flag.Parse()
	var opts []grpc.DialOption
	opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))

	var vconn *grpc.ClientConn
	var err error
	var vclient vpb.VideoServiceClient
	if server.mockVideoFlag == false {
		vconn, err = grpc.Dial(server.options.VideoServiceAddr, opts...)
		defer vconn.Close()
		vclient = vpb.NewVideoServiceClient(vconn)
		fmt.Println("Video server dialed, now trying to get video response")
	}
	if err != nil {
		//retry
		if server.options.DisableRetry == false {
			vconn, err = grpc.Dial(server.options.VideoServiceAddr, opts...)
		}
		defer vconn.Close()
		vclient = vpb.NewVideoServiceClient(vconn)
		if err != nil {
			log.Printf("fail to dial: %v", err)
			return nil, status.Error(
				codes.Unknown,
				"Video service client cannot establish connection with the server",
			)
		}
	}
	
	offset := 0
	video_infos := make([]*vpb.VideoInfo, 0)

	for offset < len(video_candids) {
		upper := int(math.Min(float64(offset + server.options.MaxBatchSize), float64(len(video_candids))))
		batch_video_candids := video_candids[offset:upper]
		video_request := &vpb.GetVideoRequest{VideoIds: batch_video_candids}

		var video_response *vpb.GetVideoResponse
		var err error
		if server.mockVideoFlag {
			video_response, err = server.mockVideoServiceClient.GetVideo(ctx, video_request)
		} else {
			video_response, err = vclient.GetVideo(ctx, video_request)
		}
		if err != nil {
			return nil, status.Error(
				codes.Unavailable,
				"Video service client fail to get the video responses from the server",
			)
		}
		video_infos = append(video_infos, video_response.GetVideos()...)
		offset += server.options.MaxBatchSize
	}
	fmt.Println("Video response got", len(video_infos), "videos")
	return video_infos, nil

}

func (server *VideoRecServiceServer) GetStats(
	ctx context.Context,
	req *pb.GetStatsRequest,
) (*pb.GetStatsResponse, error) {

	var avg_latency float32 = float32(atomic.LoadInt64(&server.totalLatency)) / float32(atomic.LoadUint64(&server.numTopVideoRequests) - atomic.LoadUint64(&server.numTopVideoErrs))
	
	return &pb.GetStatsResponse{
		TotalRequests:	atomic.LoadUint64(&server.numTopVideoRequests),
		TotalErrors:	atomic.LoadUint64(&server.numTopVideoErrs),
		ActiveRequests:	uint64(atomic.LoadInt64(&server.numActiveRequests)),
		UserServiceErrors:	atomic.LoadUint64(&server.numUserServiceErrs),
		VideoServiceErrors:	atomic.LoadUint64(&server.numVideoServiceErrs),
		StaleResponses:	atomic.LoadUint64(&server.numStaleResponse),
		AverageLatencyMs:	avg_latency,
	}, nil
}

func (server *VideoRecServiceServer) PeakTrendingVideos() ([]*vpb.VideoInfo) {
	server.fallBackVideos.mu.RLock()
	defer server.fallBackVideos.mu.RUnlock()
	return server.fallBackVideos.videos
}

func fallBack(
	server *VideoRecServiceServer,
	req *pb.GetTopVideosRequest,
) (*pb.GetTopVideosResponse, error) {
	fmt.Println("Entering Fallback")
	server.fallBackVideos.mu.Lock()
	defer server.fallBackVideos.mu.Unlock()

	if (server.fallBackVideos.videos != nil) && (len(server.fallBackVideos.videos) != 0) {
		fmt.Println("FALLBACK returning ", len(server.fallBackVideos.videos), " videos")
		atomic.AddUint64(&server.numStaleResponse, 1)

		iter_len := len(server.fallBackVideos.videos)
		if limit := req.GetLimit(); (int(limit) < iter_len && limit > 0) {
			iter_len = int(limit)
		}

		// fmt.Println("WHAT!!!!   ", atomic.LoadUint64(&server.numStaleResponse))
		return &pb.GetTopVideosResponse{Videos:server.fallBackVideos.videos[0:iter_len], StaleResponse:true}, nil
	} else {
		log.Printf("Fallback also fails")
		return nil, status.Error(
			codes.Unavailable,
			"Fallback fails, no trending video cache is available",
		)
	}
}

func fetchTrendingVideos (
	server *VideoRecServiceServer,
) {
	// Periodlly fetch trending videos
	for true {
		if server.fallBackVideos.expireTime >= time.Now().Unix() {
			time.Sleep(15 * time.Second)
			continue
		}
		flag.Parse()
		var opts []grpc.DialOption
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))

		var vconn *grpc.ClientConn
		var err error
		var vclient vpb.VideoServiceClient
		var trending_video_response *vpb.GetTrendingVideosResponse

		if server.mockVideoFlag == false {
			vconn, err = grpc.Dial(server.options.VideoServiceAddr, opts...)
			
			vclient = vpb.NewVideoServiceClient(vconn)
			fmt.Println("[Trending videos] Video server dialed, now trying to get video response")
			if err != nil {
				log.Printf("[Trending videos] fail to dial: %v", err)
				time.Sleep(15 * time.Second)
				vconn.Close()
				continue
			}
			trending_video_response, err = vclient.GetTrendingVideos(context.Background(), &vpb.GetTrendingVideosRequest{})
			
		} else {
			trending_video_response, err = server.mockVideoServiceClient.GetTrendingVideos(context.Background(), &vpb.GetTrendingVideosRequest{})
		}
		if err != nil {
			log.Printf("[Trending videos] fail to get trending video ids: %v", err)
			time.Sleep(15 * time.Second)
			vconn.Close()
			continue
		}

		// Batch
		video_ids := trending_video_response.GetVideos()
		offset := 0
		video_infos := make([]*vpb.VideoInfo, 0)
		new_expire := int64(trending_video_response.GetExpirationTimeS())

		for offset < len(video_ids) {
			upper := int(math.Min(float64(offset + server.options.MaxBatchSize), float64(len(video_ids))))
			batch_video_ids := video_ids[offset:upper]
			video_request := &vpb.GetVideoRequest{VideoIds: batch_video_ids}

			var video_response *vpb.GetVideoResponse
			var err error
			if server.mockVideoFlag {
				video_response, err = server.mockVideoServiceClient.GetVideo(context.Background(), video_request)
			} else {
				video_response, err = vclient.GetVideo(context.Background(), video_request)
			}
			if err != nil {
				log.Printf("[Trending videos] fail to get trending video infos: %v", err)
				break
			}
			video_infos = append(video_infos, video_response.GetVideos()...)
			
			offset += server.options.MaxBatchSize
		}

		server.fallBackVideos.mu.Lock()
		fmt.Println("[Trending Videos] Update cache")
		server.fallBackVideos.videos = video_infos
		server.fallBackVideos.expireTime = new_expire
		server.fallBackVideos.mu.Unlock()
		if server.mockVideoFlag == false {vconn.Close()}
	}
}

func deduplicateIds(ids []uint64) []uint64 {
	deduped := make([]uint64, 0)
	set := make(map[uint64]struct{}, 0)
	for _, id := range ids {
		if _, in_set := set[id]; !in_set {
			set[id] = struct{}{}
			deduped = append(deduped, id)
		}
	}
	return deduped
}
