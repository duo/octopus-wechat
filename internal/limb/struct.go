package limb

type GetQRCodeResp struct {
	Message string `json:"msg"`
	Result  string `json:"result"`
}

type IsLoginResp struct {
	IsLogin int    `json:"is_login"`
	Result  string `json:"result"`
}

type GetSelfResp struct {
	Data   UserInfo `json:"data"`
	Result string   `json:"result"`
}

type GetGroupMembersResp struct {
	Members string `json:"members"`
	Result  string `json:"result"`
}

type ContactResp struct {
	Data   [][5]string `json:"data,omitempty"`
	Result string      `json:"result"`
}

type UserInfo struct {
	ID        string `json:"wxId"`
	Nickname  string `json:"wxNickName"`
	BigAvatar string `json:"wxBigAvatar"`
	Remark    string `json:"wxRemark"`
}

type GroupInfo struct {
	ID        string   `json:"wxId"`
	Name      string   `json:"wxNickName"`
	BigAvatar string   `json:"wxBigAvatar"`
	Notice    string   `json:"notice"`
	Members   []string `json:"members"`
}

type WechatMessage struct {
	PID           int    `json:"pid"`
	MsgID         uint64 `json:"msgid"`
	Time          string `json:"time"`
	Timestamp     int64  `json:"timestamp"`
	WxID          string `json:"wxid"`
	Sender        string `json:"sender"`
	Self          string `json:"self"`
	IsSendMsg     int8   `json:"isSendMsg"`
	IsSendByPhone int8   `json:"isSendByPhone"`
	MsgType       int    `json:"type"`
	Message       string `json:"message"`
	FilePath      string `json:"filepath"`
	Thumbnail     string `json:"thumb_path"`
	ExtraInfo     string `json:"extrainfo"`
}
