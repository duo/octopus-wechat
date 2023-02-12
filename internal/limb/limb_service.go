package limb

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"runtime/debug"
	"sync"
	"time"

	"github.com/duo/octopus-wechat/internal/common"

	"github.com/gorilla/websocket"

	log "github.com/sirupsen/logrus"
)

const (
	defaultReconnectBackoff = 2 * time.Second
	maxReconnectBackoff     = 1 * time.Minute
	reconnectBackoffReset   = 3 * time.Minute

	closeTimeout = 3 * time.Second
)

var (
	ErrWebsocketManualStop   = errors.New("the websocket was disconnected manually")
	ErrWebsocketOverridden   = errors.New("a new call to StartWebsocket overrode the previous connection")
	ErrWebsocketUnknownError = errors.New("an unknown error occurred")

	ErrWebsocketNotConnected = errors.New("websocket not connected")
	ErrWebsocketClosed       = errors.New("websocket closed before response received")
)

type LimbService struct {
	config *common.Configure

	bot *Bot

	ws *websocket.Conn

	shortCircuitReconnectBackoff chan struct{}
	websocketStarted             chan struct{}
	websocketStopped             chan struct{}
	stopPinger                   chan struct{}
	stopping                     bool

	wsWriteLock   sync.Mutex
	StopWebsocket func(error)
}

func (ls *LimbService) Start() {
	ls.bot.Login()

	// connect to master
	go func() {
		reconnectBackoff := defaultReconnectBackoff
		lastDisconnect := time.Now().UnixNano()

		defer func() {
			close(ls.websocketStopped)
		}()

		for {
			err := ls.startWebsocket(ls.bot.getVendor())
			if err == ErrWebsocketManualStop {
				return
			} else if err != nil {
				log.Errorln("Error in LimbService websocket:", err)
			}
			if ls.stopping {
				return
			}

			now := time.Now().UnixNano()
			if lastDisconnect+reconnectBackoffReset.Nanoseconds() < now {
				reconnectBackoff = defaultReconnectBackoff
			} else {
				reconnectBackoff *= 2
				if reconnectBackoff > maxReconnectBackoff {
					reconnectBackoff = maxReconnectBackoff
				}
			}
			lastDisconnect = now
			log.Infof("Websocket disconnected, reconnecting in %d seconds...", int(reconnectBackoff.Seconds()))

			select {
			case <-ls.shortCircuitReconnectBackoff:
				log.Debugln("Reconnect backoff was short-circuited")
			case <-time.After(reconnectBackoff):
			}

			if ls.stopping {
				return
			}
		}
	}()

	// start pinger
	go func() {
		pingInterval := ls.config.Service.PingInterval
		clock := time.NewTicker(pingInterval)
		defer func() {
			log.Infoln("LimbService pinger stopped")
			clock.Stop()
		}()
		log.Infof("Pinging LimbService every %s", pingInterval)
		for {
			select {
			case <-clock.C:
				ls.ping()
			case <-ls.stopPinger:
				return
			}
			if ls.stopping {
				return
			}
		}
	}()

	ls.bot.Start()
}

func (ls *LimbService) Stop() {
	log.Infoln("LimbService stopping")

	ls.stopping = true

	ls.bot.Stop()

	select {
	case ls.stopPinger <- struct{}{}:
	default:
	}

	if ls.StopWebsocket != nil {
		ls.StopWebsocket(ErrWebsocketManualStop)
	}

	// Short-circuit reconnect backoff so the websocket loop exits even if it's disconnected
	select {
	case ls.shortCircuitReconnectBackoff <- struct{}{}:
	default:
	}

	select {
	case <-ls.websocketStopped:
	case <-time.After(7 * time.Second):
		log.Warnln("Timed out waiting for websocket to close")
	}
}

func NewLimbService(config *common.Configure) *LimbService {
	service := &LimbService{
		config: config,

		shortCircuitReconnectBackoff: make(chan struct{}),
		websocketStarted:             make(chan struct{}),
		websocketStopped:             make(chan struct{}),
		stopPinger:                   make(chan struct{}),
	}
	service.bot = NewBot(config, service.pushEvent)

	return service
}

func (ls *LimbService) startWebsocket(vendor common.Vendor) error {
	ws, resp, err := websocket.DefaultDialer.Dial(ls.config.Service.Addr, http.Header{
		"Authorization": []string{fmt.Sprintf("Basic %s", ls.config.Service.Secret)},
		"Vendor":        []string{vendor.String()},
	})
	if resp != nil && resp.StatusCode >= 400 {
		var errResp common.ErrorResponse
		err = json.NewDecoder(resp.Body).Decode(&errResp)
		if err != nil {
			return fmt.Errorf("websocket request returned HTTP %d with non-JSON body", resp.StatusCode)
		} else {
			return fmt.Errorf("websocket request returned %s (HTTP %d): %s", errResp.Code, resp.StatusCode, errResp.Message)
		}
	} else if err != nil {
		return fmt.Errorf("failed to open websocket: %w", err)
	}

	if ls.StopWebsocket != nil {
		ls.StopWebsocket(ErrWebsocketOverridden)
	}

	closeChan := make(chan error)
	closeChanOnce := sync.Once{}
	stopFunc := func(err error) {
		closeChanOnce.Do(func() {
			closeChan <- err
		})
	}

	ls.ws = ws
	ls.StopWebsocket = stopFunc
	log.Infoln("LimbService websocket connected")

	go ls.consumeWebsocket(stopFunc, ws)

	select {
	case ls.websocketStarted <- struct{}{}:
	default:
	}

	closeErr := <-closeChan

	if ls.ws == ws {
		ls.ws = nil
	}

	_ = ws.SetWriteDeadline(time.Now().Add(closeTimeout))
	err = ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseGoingAway, ""))
	if err != nil && !errors.Is(err, websocket.ErrCloseSent) {
		log.Warnf("Error writing close message to websocket: %v", err)
	}
	err = ws.Close()
	if err != nil {
		log.Warnf("Error closing websocket: %v", err)
	} else {
		log.Infoln("LimbService websocket closed")
	}

	return closeErr
}

// read messages from master
func (ls *LimbService) consumeWebsocket(stopFunc func(error), ws *websocket.Conn) {
	defer stopFunc(ErrWebsocketUnknownError)
	for {
		var msg common.OctopusMessage
		err := ws.ReadJSON(&msg)
		if err != nil {
			log.Debugln("Error reading from websocket:", err)
			stopFunc(err)
			return
		}

		switch msg.Type {
		case common.MsgRequest:
			request := msg.Data.(*common.OctopusRequest)
			go ls.processRequest(msg.ID, request)
		case common.MsgResponse:
			response := msg.Data.(*common.OctopusResponse)
			log.Debugf("Receive response for #%d %s", msg.ID, response.Type)
		}
	}
}

// process requests from master
func (ls *LimbService) processRequest(id int64, req *common.OctopusRequest) {
	defer func() {
		panicErr := recover()
		if panicErr != nil {
			log.Errorf("Panic while responding to command %s in request #%d: %v\n%s", req.Type, id, panicErr, debug.Stack())
		}
	}()

	resp := ls.actuallyHandleRequest(req)
	if id != 0 {
		respMsg := &common.OctopusMessage{
			ID:   id,
			Type: common.MsgResponse,
			Data: resp,
		}
		ls.wsWriteLock.Lock()
		defer ls.wsWriteLock.Unlock()
		if err := ls.ws.WriteJSON(respMsg); err != nil {
			log.Warnf("Failed to send response to req #%d: %v", id, err)
		}
	}
}

func (ls *LimbService) actuallyHandleRequest(req *common.OctopusRequest) *common.OctopusResponse {
	switch req.Type {
	case common.ReqEvent:
		if ret, err := ls.bot.processOcotopusEvent(req.Data.(*common.OctopusEvent)); err != nil {
			return &common.OctopusResponse{
				Type: common.RespEvent,
				Error: &common.ErrorResponse{
					Code:    "O_EVENT_FAILED",
					Message: err.Error(),
				},
			}
		} else {
			return &common.OctopusResponse{
				Type: common.RespEvent,
				Data: ret,
			}
		}
	default:
		return nil
	}
}

// push event ro master
func (ls *LimbService) pushEvent(event *common.OctopusEvent) {
	ws := ls.ws

	if ws == nil {
		log.Warnln("Failed to push event: websocket not connected")
		return
	}

	msg := &common.OctopusMessage{
		Type: common.MsgRequest,
		Data: &common.OctopusRequest{
			Type: common.ReqEvent,
			Data: event,
		},
	}

	go func() {
		ls.wsWriteLock.Lock()
		defer ls.wsWriteLock.Unlock()
		log.Debugf("Push event: %+v", event)
		_ = ls.ws.SetWriteDeadline(time.Now().Add(ls.config.Service.SendTiemout))
		err := ls.ws.WriteJSON(msg)
		if err != nil {
			log.Warnf("Failed to push event %s: %v", event.ID, err)
		}
	}()
}

func (ls *LimbService) sendPing() error {
	ws := ls.ws

	if ws == nil {
		return ErrWebsocketNotConnected
	}

	ls.wsWriteLock.Lock()
	defer ls.wsWriteLock.Unlock()
	_ = ws.SetWriteDeadline(time.Now().Add(ls.config.Service.SendTiemout))
	return ws.WriteJSON(&common.OctopusMessage{
		Type: common.MsgRequest,
		Data: &common.OctopusRequest{
			Type: common.ReqPing,
		},
	})
}

func (ls *LimbService) ping() {
	if ls.ws == nil {
		pingInterval := ls.config.Service.PingInterval
		log.Debugln("Received server ping request, but no websocket connected. Trying to short-circuit backoff sleep")
		select {
		case ls.shortCircuitReconnectBackoff <- struct{}{}:
		default:
			log.Warnln("Failed to ping websocket: not connected and no backoff?")
			return
		}
		select {
		case <-ls.websocketStarted:
		case <-time.After(pingInterval):
			if ls.ws == nil {
				log.Warnf("Failed to ping websocket: didn't connect after %s of waiting", pingInterval)
				return
			}
		}
	}

	if err := ls.sendPing(); err != nil {
		log.Warnf("Websocket ping returned error: %v", err)
		ls.StopWebsocket(fmt.Errorf("websocket ping returned error: %w", err))
	}
}
