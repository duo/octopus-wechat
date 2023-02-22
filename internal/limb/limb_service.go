package limb

import (
	"fmt"
	"net/http"
	"runtime/debug"

	"github.com/duo/octopus-wechat/internal/common"

	"github.com/duo/wsc"

	log "github.com/sirupsen/logrus"
)

type LimbService struct {
	config *common.Configure

	bot *Bot

	clientOptions *wsc.ClientOptions
	client        *wsc.Client
}

func (ls *LimbService) Start() {
	ls.bot.Login()

	ls.clientOptions.HTTPHeaders = http.Header{
		"Authorization": []string{fmt.Sprintf("Basic %s", ls.config.Service.Secret)},
		"Vendor":        []string{ls.bot.getVendor().String()},
	}
	if err := ls.client.Connect(); err != nil {
		log.Fatal(err)
	}
	log.Infof("Connected to %s", ls.config.Service.Addr)

	ls.bot.Start()
}

func (ls *LimbService) Stop() {
	log.Infoln("LimbService stopping")

	ls.bot.Stop()
	ls.client.Disconnect()
}

func NewLimbService(config *common.Configure) *LimbService {
	options, err := wsc.NewClientOptions(config.Service.Addr)
	if err != nil {
		log.Fatal(err)
	}

	service := &LimbService{
		config:        config,
		clientOptions: options,
		client:        wsc.NewClient(options),
	}

	options.OnConnected = service.consumeWebsocket
	service.bot = NewBot(config, service.pushEvent)

	return service
}

// read messages from master
func (ls *LimbService) consumeWebsocket(client *wsc.Client) {
	for {
		var msg common.OctopusMessage
		err := ls.client.ReadJSON(&msg)
		if err != nil {
			log.Debugln("Error reading from websocket:", err)
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
		if err := ls.client.WriteJSON(respMsg); err != nil {
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
	msg := &common.OctopusMessage{
		Type: common.MsgRequest,
		Data: &common.OctopusRequest{
			Type: common.ReqEvent,
			Data: event,
		},
	}

	go func() {
		log.Debugf("Push event: %+v", event)
		if err := ls.client.WriteJSON(msg); err != nil {
			log.Warnf("Failed to push event %s: %v", event.ID, err)
		}
	}()
}
