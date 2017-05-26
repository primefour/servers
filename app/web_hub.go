// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See License.txt for license information.

package app

import (
	"fmt"
	"hash/fnv"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	l4g "github.com/alecthomas/log4go"

	"github.com/primefour/servers/einterfaces"
	"github.com/primefour/servers/model"
	"github.com/primefour/servers/utils"
)

const (
	BROADCAST_QUEUE_SIZE = 4096
	DEADLOCK_TICKER      = 15 * time.Second                  // check every 15 seconds
	DEADLOCK_WARN        = (BROADCAST_QUEUE_SIZE * 99) / 100 // number of buffered messages before printing stack trace
)

type Hub struct {
	connections     []*WebConn
	connectionCount int64
	connectionIndex int
	register        chan *WebConn
	unregister      chan *WebConn
	broadcast       chan *model.WebSocketEvent
	stop            chan string
	invalidateUser  chan string
	ExplicitStop    bool
	goroutineId     int
}

var hubs []*Hub = make([]*Hub, 0)
var stopCheckingForDeadlock chan bool

func NewWebHub() *Hub {
	return &Hub{
		register:       make(chan *WebConn),
		unregister:     make(chan *WebConn),
		connections:    make([]*WebConn, 0, model.SESSION_CACHE_SIZE),
		broadcast:      make(chan *model.WebSocketEvent, BROADCAST_QUEUE_SIZE),
		stop:           make(chan string),
		invalidateUser: make(chan string),
		ExplicitStop:   false,
	}
}

func TotalWebsocketConnections() int {
	count := int64(0)
	for _, hub := range hubs {
		count = count + atomic.LoadInt64(&hub.connectionCount)
	}

	return int(count)
}

func HubStart() {
	l4g.Info(utils.T("api.web_hub.start.starting.debug"), runtime.NumCPU()*2)

	// Total number of hubs is twice the number of CPUs.
	hubs = make([]*Hub, runtime.NumCPU()*2)

	for i := 0; i < len(hubs); i++ {
		hubs[i] = NewWebHub()
		hubs[i].connectionIndex = i
		hubs[i].Start()
	}

	go func() {
		ticker := time.NewTicker(DEADLOCK_TICKER)

		defer func() {
			ticker.Stop()
		}()

		stopCheckingForDeadlock = make(chan bool, 1)

		for {
			select {
			case <-ticker.C:
				for _, hub := range hubs {
					if len(hub.broadcast) >= DEADLOCK_WARN {
						l4g.Error("Hub processing might be deadlock on hub %v goroutine %v with %v events in the buffer", hub.connectionIndex, hub.goroutineId, len(hub.broadcast))
						buf := make([]byte, 1<<16)
						runtime.Stack(buf, true)
						output := fmt.Sprintf("%s", buf)
						splits := strings.Split(output, "goroutine ")

						for _, part := range splits {
							if strings.Index(part, fmt.Sprintf("%v", hub.goroutineId)) > -1 {
								l4g.Error("Trace for possible deadlock goroutine %v", part)
							}
						}
					}
				}

			case <-stopCheckingForDeadlock:
				return
			}
		}
	}()
}

func HubStop() {
	l4g.Info(utils.T("api.web_hub.start.stopping.debug"))

	select {
	case stopCheckingForDeadlock <- true:
	default:
		l4g.Warn("We appear to have already sent the stop checking for deadlocks command")
	}

	for _, hub := range hubs {
		hub.Stop()
	}

	hubs = make([]*Hub, 0)
}

func GetHubForUserId(userId string) *Hub {
	hash := fnv.New32a()
	hash.Write([]byte(userId))
	index := hash.Sum32() % uint32(len(hubs))
	return hubs[index]
}

func HubRegister(webConn *WebConn) {
	GetHubForUserId(webConn.UserId).Register(webConn)
}

func HubUnregister(webConn *WebConn) {
	GetHubForUserId(webConn.UserId).Unregister(webConn)
}

func Publish(message *model.WebSocketEvent) {

	if metrics := einterfaces.GetMetricsInterface(); metrics != nil {
		metrics.IncrementWebsocketEvent(message.Event)
	}

	for _, hub := range hubs {
		hub.Broadcast(message)
	}

	if einterfaces.GetClusterInterface() != nil {
		einterfaces.GetClusterInterface().Publish(message)
	}
}

func PublishSkipClusterSend(message *model.WebSocketEvent) {
	for _, hub := range hubs {
		hub.Broadcast(message)
	}
}

func InvalidateCacheForChannel(channel *model.Channel) {
	InvalidateCacheForChannelSkipClusterSend(channel.Id)
	InvalidateCacheForChannelByNameSkipClusterSend(channel.TeamId, channel.Name)

	if cluster := einterfaces.GetClusterInterface(); cluster != nil {
		cluster.InvalidateCacheForChannel(channel.Id)
		cluster.InvalidateCacheForChannelByName(channel.TeamId, channel.Name)
	}
}

func InvalidateCacheForChannelMembers(channelId string) {
	InvalidateCacheForChannelMembersSkipClusterSend(channelId)

	if cluster := einterfaces.GetClusterInterface(); cluster != nil {
		cluster.InvalidateCacheForChannelMembers(channelId)
	}
}

func InvalidateCacheForChannelSkipClusterSend(channelId string) {
	Srv.Store.Channel().InvalidateChannel(channelId)
}

func InvalidateCacheForChannelMembersSkipClusterSend(channelId string) {
	Srv.Store.User().InvalidateProfilesInChannelCache(channelId)
	Srv.Store.Channel().InvalidateMemberCount(channelId)
}

func InvalidateCacheForChannelMembersNotifyProps(channelId string) {
	InvalidateCacheForChannelMembersNotifyPropsSkipClusterSend(channelId)

	if cluster := einterfaces.GetClusterInterface(); cluster != nil {
		cluster.InvalidateCacheForChannelMembersNotifyProps(channelId)
	}
}

func InvalidateCacheForChannelMembersNotifyPropsSkipClusterSend(channelId string) {
	Srv.Store.Channel().InvalidateCacheForChannelMembersNotifyProps(channelId)
}

func InvalidateCacheForChannelByNameSkipClusterSend(teamId, name string) {
	Srv.Store.Channel().InvalidateChannelByName(teamId, name)
}

func InvalidateCacheForChannelPosts(channelId string) {
	InvalidateCacheForChannelPostsSkipClusterSend(channelId)

	if cluster := einterfaces.GetClusterInterface(); cluster != nil {
		cluster.InvalidateCacheForChannelPosts(channelId)
	}
}

func InvalidateCacheForChannelPostsSkipClusterSend(channelId string) {
	Srv.Store.Post().InvalidateLastPostTimeCache(channelId)
}

func InvalidateCacheForUser(userId string) {
	InvalidateCacheForUserSkipClusterSend(userId)

	if einterfaces.GetClusterInterface() != nil {
		einterfaces.GetClusterInterface().InvalidateCacheForUser(userId)
	}
}

func InvalidateCacheForUserSkipClusterSend(userId string) {
	Srv.Store.Channel().InvalidateAllChannelMembersForUser(userId)
	Srv.Store.User().InvalidateProfilesInChannelCacheByUser(userId)
	Srv.Store.User().InvalidatProfileCacheForUser(userId)

	if len(hubs) != 0 {
		GetHubForUserId(userId).InvalidateUser(userId)
	}
}

func InvalidateCacheForWebhook(webhookId string) {
	InvalidateCacheForWebhookSkipClusterSend(webhookId)

	if cluster := einterfaces.GetClusterInterface(); cluster != nil {
		cluster.InvalidateCacheForWebhook(webhookId)
	}
}

func InvalidateCacheForWebhookSkipClusterSend(webhookId string) {
	Srv.Store.Webhook().InvalidateWebhookCache(webhookId)
}

func InvalidateWebConnSessionCacheForUser(userId string) {
	if len(hubs) != 0 {
		GetHubForUserId(userId).InvalidateUser(userId)
	}
}

func InvalidateCacheForReactions(postId string) {
	InvalidateCacheForReactionsSkipClusterSend(postId)

	if cluster := einterfaces.GetClusterInterface(); cluster != nil {
		cluster.InvalidateCacheForReactions(postId)
	}
}

func InvalidateCacheForReactionsSkipClusterSend(postId string) {
	Srv.Store.Reaction().InvalidateCacheForPost(postId)
}

func (h *Hub) Register(webConn *WebConn) {
	h.register <- webConn

	if webConn.IsAuthenticated() {
		webConn.SendHello()
	}
}

func (h *Hub) Unregister(webConn *WebConn) {
	h.unregister <- webConn
}

func (h *Hub) Broadcast(message *model.WebSocketEvent) {
	if message != nil {
		h.broadcast <- message
	}
}

func (h *Hub) InvalidateUser(userId string) {
	h.invalidateUser <- userId
}

func getGoroutineId() int {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	idField := strings.Fields(strings.TrimPrefix(string(buf[:n]), "goroutine "))[0]
	id, err := strconv.Atoi(idField)
	if err != nil {
		id = -1
	}
	return id
}

func (h *Hub) Stop() {
	h.stop <- "all"
}

func (h *Hub) Start() {
	var doStart func()
	var doRecoverableStart func()
	var doRecover func()

	doStart = func() {

		h.goroutineId = getGoroutineId()
		l4g.Debug("Hub for index %v is starting with goroutine %v", h.connectionIndex, h.goroutineId)

		for {
			select {
			case webCon := <-h.register:
				h.connections = append(h.connections, webCon)
				atomic.StoreInt64(&h.connectionCount, int64(len(h.connections)))

			case webCon := <-h.unregister:
				userId := webCon.UserId

				found := false
				indexToDel := -1
				for i, webConCandidate := range h.connections {
					if webConCandidate == webCon {
						indexToDel = i
						continue
					}
					if userId == webConCandidate.UserId {
						found = true
						if indexToDel != -1 {
							break
						}
					}
				}

				if indexToDel != -1 {
					// Delete the webcon we are unregistering
					h.connections[indexToDel] = h.connections[len(h.connections)-1]
					h.connections = h.connections[:len(h.connections)-1]
				}

				if len(userId) == 0 {
					continue
				}

				if !found {
					go SetStatusOffline(userId, false)
				}

			case userId := <-h.invalidateUser:
				for _, webCon := range h.connections {
					if webCon.UserId == userId {
						webCon.InvalidateCache()
					}
				}

			case msg := <-h.broadcast:
				for _, webCon := range h.connections {
					if webCon.ShouldSendEvent(msg) {
						select {
						case webCon.Send <- msg:
						default:
							l4g.Error(fmt.Sprintf("webhub.broadcast: cannot send, closing websocket for userId=%v", webCon.UserId))
							close(webCon.Send)
							for i, webConCandidate := range h.connections {
								if webConCandidate == webCon {
									h.connections[i] = h.connections[len(h.connections)-1]
									h.connections = h.connections[:len(h.connections)-1]
									break
								}
							}
						}
					}
				}

			case <-h.stop:
				for _, webCon := range h.connections {
					webCon.WebSocket.Close()
				}
				h.ExplicitStop = true

				return
			}
		}
	}

	doRecoverableStart = func() {
		defer doRecover()
		doStart()
	}

	doRecover = func() {
		if !h.ExplicitStop {
			if r := recover(); r != nil {
				l4g.Error(fmt.Sprintf("Recovering from Hub panic. Panic was: %v", r))
			} else {
				l4g.Error("Webhub stopped unexpectedly. Recovering.")
			}

			l4g.Error(string(debug.Stack()))

			go doRecoverableStart()
		}
	}

	go doRecoverableStart()
}
