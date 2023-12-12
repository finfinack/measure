package data

const (
	MethodNotifyFullStatus = "NotifyFullStatus"
	MethodNotifyStatus     = "NotifyStatus"
	MethodNotifyEvent      = "NotifyEvent"
)

type WSMessage struct {
	Src    string `json:"src"`    // "src":"shellyplusht-..."
	Dst    string `json:"dst"`    // "dst":"ws"
	Method string `json:"method"` // "method":"NotifyFullStatus"
}
