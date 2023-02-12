package limb

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/duo/octopus-wechat/internal/common"

	"github.com/shirou/gopsutil/v3/process"
	"github.com/tidwall/tinylru"

	log "github.com/sirupsen/logrus"
)

type Bot struct {
	config *common.Configure

	funcNewWechat   uintptr
	funcStartListen uintptr
	funcStopListen  uintptr

	workdir string
	docdir  string

	listener net.Listener

	me     *UserInfo
	client *WechatClient

	history tinylru.LRU
	mutex   common.KeyMutex

	pushFunc func(*common.OctopusEvent)

	stopping bool
	stopSync chan struct{}
}

func (b *Bot) Login() {
	b.client = NewWechatClient(b.config)

	// start WeChat client
	pid, _, errno := syscall.SyscallN(b.funcNewWechat)
	if pid == 0 {
		log.Fatal(errno)
	}
	if int(errno) != 0 {
		log.Warnf("Start WeChat encountered issue: %v", errno)
	}
	b.client.pid = pid

	if p, err := process.NewProcess(int32(pid)); err != nil {
		log.Fatal(err)
	} else {
		b.client.proc = p
	}

	_, _, errno = syscall.SyscallN(b.funcStartListen, pid, uintptr(b.client.port))
	if int(errno) != 0 {
		b.client.Dispose()
		log.Fatal(errno)
	}

	if err := b.hook(); err != nil {
		b.client.Dispose()
		log.Fatal(err)
	}

	// TODO: render the QR code?
	if _, err := b.client.LoginWtihQRCode(); err != nil {
		b.client.Dispose()
		log.Fatalln("Failed to switch to QR login")
	}

	if err := b.ensureLogin(); err != nil {
		b.client.Dispose()
		log.Fatal(err)
	}

	if info, err := b.client.GetSelf(); err != nil {
		b.client.Dispose()
		log.Fatal(err)
	} else {
		b.me = info
	}
}

func (b *Bot) Start() {
	log.Infoln("Bot started")

	go func() {
		time.Sleep(b.config.Service.SyncDelay)
		go b.sync()

		clock := time.NewTicker(b.config.Service.SyncInterval)
		defer func() {
			log.Infoln("LimbService sync stopped")
			clock.Stop()
		}()
		log.Infof("Syncing LimbService every %s", b.config.Service.SyncInterval)
		for {
			select {
			case <-clock.C:
				go b.sync()
			case <-b.stopSync:
				return
			}
		}
	}()
}

func (b *Bot) Stop() {
	log.Infoln("Bot stopping")

	b.stopping = true

	select {
	case b.stopSync <- struct{}{}:
	default:
	}

	if b.client != nil {
		b.client.Dispose()
	}

	if b.listener != nil {
		b.listener.Close()
	}
}

func NewBot(config *common.Configure, pushFunc func(*common.OctopusEvent)) *Bot {
	driver := LoadDriver()
	defer syscall.FreeLibrary(driver)

	newWechat, err := syscall.GetProcAddress(driver, "new_wechat")
	if err != nil {
		log.Fatal(err)
	}
	startListen, err := syscall.GetProcAddress(driver, "start_listen")
	if err != nil {
		log.Fatal(err)
	}
	stopListen, err := syscall.GetProcAddress(driver, "stop_listen")
	if err != nil {
		log.Fatal(err)
	}

	workdir := filepath.Join(getDocDir(), "matrix_wechat_agent")
	if !pathExists(workdir) {
		if err := os.MkdirAll(workdir, 0o644); err != nil {
			log.Fatalf("Failed to create temp folder: %v", err)
		}
	}

	bot := &Bot{
		config:          config,
		funcNewWechat:   newWechat,
		funcStartListen: startListen,
		funcStopListen:  stopListen,
		workdir:         workdir,
		docdir:          getWechatDocdir(),
		mutex:           common.NewHashed(47),
		pushFunc:        pushFunc,
		stopSync:        make(chan struct{}),
	}

	go bot.serve()

	return bot
}

func (b *Bot) sync() {
	event := b.generateEvent("sync", time.Now().UnixMilli())

	chats := []*common.Chat{}

	if info, err := b.client.GetSelf(); err != nil {
		log.Warnf("Failed to get self info: %v", err)
		return
	} else {
		b.me = info
		chats = append(chats, &common.Chat{
			ID:    info.ID,
			Type:  "private",
			Title: info.Nickname,
		})
	}
	if infos, err := b.client.GetFriendList(); err != nil {
		log.Warnf("Failed to get friend list: %v", err)
	} else {
		for _, info := range infos {
			title := info.ID
			if info.Nickname != "" {
				title = info.Nickname
			}
			if info.Remark != "" {
				title = info.Remark
			}
			chats = append(chats, &common.Chat{
				ID:    info.ID,
				Type:  "private",
				Title: title,
			})
		}
	}
	if infos, err := b.client.GetGroupList(); err != nil {
		log.Warnf("Failed to get group list: %v", err)
	} else {
		for _, info := range infos {
			name := info.ID
			if info.Name != "" {
				name = info.Name
			}
			chats = append(chats, &common.Chat{
				ID:    info.ID,
				Type:  "group",
				Title: name,
			})
		}
	}

	event.Type = common.EventSync
	event.Data = chats

	b.pushFunc(event)
}

// process events from master
func (b *Bot) processOcotopusEvent(event *common.OctopusEvent) (*common.OctopusEvent, error) {
	log.Debugf("Receive Octopus event: %+v", event)

	var err error
	target := event.Chat.ID
	switch event.Type {
	case common.EventText:
		err = b.client.SendText(target, event.Content)
	case common.EventPhoto, common.EventVideo:
		path := saveBlob(b, event)
		if len(path) > 0 {
			err = b.client.SendImage(target, path)
		} else {
			err = fmt.Errorf("failed to download media")
		}
	case common.EventFile:
		path := saveBlob(b, event)
		if len(path) > 0 {
			err = b.client.SendFile(target, path)
		} else {
			err = fmt.Errorf("failed to download file")
		}
	default:
		err = fmt.Errorf("event type not support: %s", event.Type)
	}

	return &common.OctopusEvent{
		ID:        common.Itoa(time.Now().UnixMilli()),
		Timestamp: time.Now().Unix(),
	}, err
}

// receive WeChat tcp package
func (b *Bot) serve() {
	addr := fmt.Sprintf("127.0.0.1:%d", b.config.Limb.ListenPort)
	log.Infof("Bot starting to listen on %s", addr)

	listenr, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalln("Error in listener:", err)
	}
	b.listener = listenr

	for {
		conn, err := listenr.Accept()
		if err != nil {
			log.Warnf("Failed to accept: %v", err)
			if b.stopping {
				return
			}
			break
		}

		go func(conn net.Conn) {
			defer conn.Close()

			for {
				data, err := bufio.NewReader(conn).ReadBytes('\n')
				if err != nil {
					if err != io.EOF {
						log.Fatal(err)
					}
					return
				}

				//log.Debugf("Receive data: %s", string(data))

				msg := WechatMessage{
					IsSendByPhone: 1,
				}
				if err := json.Unmarshal(data, &msg); err != nil {
					log.Warnf("Failed to unmarshal data from WeChat: %v", err)
					conn.Write([]byte("500 ERROR"))
				} else {
					go func() {
						b.mutex.LockKey(msg.Sender)
						defer b.mutex.UnlockKey(msg.Sender)

						b.processWechatMessage(&msg)
					}()
					conn.Write([]byte("200 OK"))
				}
			}
		}(conn)
	}
}

// process WeChat message
func (b *Bot) processWechatMessage(msg *WechatMessage) {
	log.Debugf("Receive WeChat msg: %+v", msg)

	if b.me == nil {
		log.Warnln("WeChat processor not ready")
		return
	}

	// Skip message sent by hook
	if msg.IsSendByPhone == 0 && msg.MsgType != 10000 {
		b.history.Set(msg.MsgID, struct{}{})
		return
	} else if _, ok := b.history.Get(msg.MsgID); ok {
		return
	}

	event := b.generateEvent(fmt.Sprint(msg.MsgID), msg.Timestamp)

	event.Type = common.EventText
	event.Content = msg.Message

	switch msg.MsgType {
	case 0: // unknown
		return
	case 1: // Txt
	case 3: // Image
		if len(msg.FilePath) == 0 {
			return
		}
		blob := downloadImage(b, msg)
		if blob != nil {
			event.Type = common.EventPhoto
			event.Data = []*common.BlobData{blob}
			event.Content = ""
		} else {
			event.Content = "[图片下载失败]"
		}
	case 34: // Voice
		blob := downloadVoice(b, msg)
		if blob != nil {
			event.Type = common.EventAudio
			event.Data = blob
			event.Content = ""
		} else {
			event.Content = "[语音下载失败]"
		}
	case 42: // Card
		if card := parseCard(b, msg); card != nil {
			event.Type = common.EventApp
			event.Data = card
		} else {
			event.Content = "[名片解析失败]"
		}
	case 43: // Video
		if len(msg.FilePath) == 0 && len(msg.Thumbnail) == 0 {
			return
		}
		if _, ok := b.history.Get(msg.MsgID); ok {
			return
		}
		b.history.Set(msg.MsgID, struct{}{})

		blob := downloadVideo(b, msg)
		if blob != nil {
			event.Type = common.EventVideo
			event.Data = blob
			event.Content = ""
		} else {
			event.Content = "[视频下载失败]"
		}
	case 47: // Sticker
		blob := downloadSticker(b, msg)
		if blob != nil {
			event.Type = common.EventPhoto
			event.Data = []*common.BlobData{blob}
			event.Content = ""
		} else {
			event.Content = "[表情下载失败]"
		}
	case 48: // Location
		location := parseLocation(b, msg)
		if location != nil {
			event.Type = common.EventLocation
			event.Data = location
		} else {
			event.Content = "[位置解析失败]"
		}
	case 49: // App
		appType := getAppType(b, msg)
		switch appType {
		case 6: // File
			if len(msg.FilePath) == 0 {
				return
			}
			// FIXME: skip duplicate file
			if !strings.HasPrefix(msg.Message, "<?xml") {
				return
			}
			if _, ok := b.history.Set(msg.MsgID, struct{}{}); ok {
				return
			}
			blob := downloadFile(b, msg)
			if blob != nil {
				event.Type = common.EventFile
				event.Data = blob
				event.Content = ""
			} else {
				event.Content = "[文件下载失败]"
			}
		case 8:
			if len(msg.FilePath) == 0 {
				return
			}
			// FIXME: skip duplicate sticker
			if strings.HasPrefix(msg.Message, "<?xml") {
				return
			}
			blob := downloadSticker(b, msg)
			if blob != nil {
				event.Type = common.EventPhoto
				event.Data = []*common.BlobData{blob}
				event.Content = ""
			} else {
				event.Content = "[表情下载失败]"
			}
		case 57: // TODO: reply meesage not found fallback
			content, reply := parseReply(b, msg)
			if len(content) > 0 && reply != nil {
				event.Content = content
				event.Reply = reply
			}
		case 87:
			content := parseNotice(b, msg)
			if len(content) > 0 {
				event.Type = common.EventNotice
				event.Content = content
			}
		//case 2000: // Transfer
		default:
			app := parseApp(b, msg, appType)
			if app != nil {
				event.Type = common.EventApp
				event.Data = app
			} else {
				event.Content = "[应用解析失败]"
			}
		}
	case 50: // private voip
		event.Type = common.EventVoIP
		event.Content = parsePrivateVoIP(b, msg)
		if event.Content == "" {
			return
		}
	case 51: // last message
		return
	case 10000: // revoke
		content := parseRevoke(b, msg)
		if len(content) > 0 {
			event.Reply = &common.ReplyInfo{
				ID: event.ID,
			}
			event.ID = common.Itoa(time.Now().UnixMilli())
			event.Type = common.EventRevoke
			event.Content = content
		} else {
			event.Type = common.EventSystem
		}
	case 10002: // system
		if msg.Sender == "weixin" || msg.IsSendMsg == 1 {
			return
		}
		event.Type = common.EventSystem
		event.Content = parseSystemMessage(b, msg)
		if len(event.Content) == 0 {
			return
		}
	}

	if msg.IsSendMsg == 0 {
		name := msg.WxID
		remark := msg.WxID
		if info, err := b.client.GetUserInfo(msg.WxID); err != nil {
			log.Warnf("Failed to get user info for %s: %v", msg.WxID, err)
		} else {
			name = info.Nickname
			remark = info.Remark
		}
		if strings.HasSuffix(msg.Sender, "@chatroom") {
			if groupNickname, err := b.client.GetGroupMemberNickname(msg.Sender, msg.WxID); err != nil {
				log.Warnf("Failed to get group nickname for %s: %v", msg.WxID, err)
			} else if groupNickname != "" {
				remark = groupNickname
			}
		}
		event.From = common.User{
			ID:       msg.WxID,
			Username: name,
			Remark:   remark,
		}
	} else {
		event.From = common.User{
			ID:       b.me.ID,
			Username: b.me.Nickname,
			Remark:   b.me.Nickname,
		}
	}

	if strings.HasSuffix(msg.Sender, "@chatroom") {
		title := msg.Sender
		if info, err := b.client.GetGroupInfo(msg.Sender); err != nil {
			log.Warnf("Failed to get group info for %s: %v", msg.Sender, err)
		} else {
			if info.Name != "" {
				title = info.Name
			}
		}
		event.Chat = common.Chat{
			Type:  "group",
			ID:    msg.Sender,
			Title: title,
		}
	} else {
		title := msg.Sender
		if info, err := b.client.GetUserInfo(msg.Sender); err != nil {
			log.Warnf("Failed to get user info for %s: %v", msg.Sender, err)
		} else {
			if info.Nickname != "" {
				title = info.Nickname
			}
			if info.Remark != "" {
				title = info.Remark
			}
		}
		event.Chat = common.Chat{
			Type:  "private",
			ID:    msg.Sender,
			Title: title,
		}
	}

	b.pushFunc(event)
}

func (b *Bot) hook() error {
	ctx, cancel := context.WithTimeout(context.Background(), b.config.Limb.RequestTimeout)
	defer cancel()

	for {
		if err := b.client.HookMsg(b.workdir); err == nil {
			if err := b.client.SetVersion(b.config.Limb.Version); err != nil {
				log.Warnf("Failed to set WeChat version: %v", err)
			} else {
				log.Infof("Set WeChat version to %s", b.config.Limb.Version)
			}
			return nil
		}

		select {
		case <-time.After(3 * time.Second):
		case <-ctx.Done():
			return fmt.Errorf("failed to hook WeChat")
		}
	}
}

func (b *Bot) ensureLogin() error {
	ctx, cancel := context.WithTimeout(context.Background(), b.config.Limb.RequestTimeout)
	defer cancel()

	for {
		if b.client.IsLogin() {
			return nil
		}
		select {
		case <-time.After(3 * time.Second):
		case <-ctx.Done():
			return fmt.Errorf("failed to ensure WeChat is login")
		}
	}
}

func (b *Bot) generateEvent(id string, ts int64) *common.OctopusEvent {
	return &common.OctopusEvent{
		Vendor:    b.getVendor(),
		ID:        id,
		Timestamp: ts,
	}
}

func (b *Bot) getVendor() common.Vendor {
	return common.Vendor{
		Type: "wechat",
		UID:  b.me.ID,
	}
}
