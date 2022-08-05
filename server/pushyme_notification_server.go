package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/kyokomi/emoji"
	"io"
	"net"
	"net/http"
	"time"
)

type PushyNotificationServer struct {
	metrics           *metrics
	logger            *Logger
	PushyPushSettings PushyPushSettings
	Client            *http.Client
}

type PushyPayloadBody struct {
	Type    string `json:"type"`
	Sender  string `json:"sender"`
	Channel string `json:"channel"`
	Message string `json:"message"`
}

type PushyPayload struct {
	DeviceId string `json:"to"`
	// Body     PushyPayloadBody `json:"data"`
	// Body PushNotification `json:"data"`
	Body map[string]interface{} `json:"data"`
}

type PushyResponseInfo struct {
	Devices int `json:"devices"`
}

type PushyResponse struct {
	Succ bool   `json:"success"`
	Id   string `json:"id"`
	Info PushyResponseInfo
}

func NewPushyNotificationServer(settings PushyPushSettings, logger *Logger, metrics *metrics) NotificationServer {

	//copy from DefaultTransport
	var tr http.RoundTripper = &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          settings.MaxConns,
		MaxIdleConnsPerHost:   settings.MaxConns,                                     //DefaultTransport 100
		IdleConnTimeout:       time.Duration(settings.IdleConnTimeout) * time.Second, // DefaultTransport 90
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	client := &http.Client{Transport: tr}

	return &PushyNotificationServer{
		PushyPushSettings: settings,
		metrics:           metrics,
		logger:            logger,
		Client:            client,
	}
}
func (me *PushyNotificationServer) Initialize() bool {
	me.logger.Infof(fmt.Sprintf("Initializing Pushy notification server for %v", me.PushyPushSettings.ReplaceForType))

	if me.PushyPushSettings.SecretAPIKey == "" {
		me.logger.Error("Pushy push notifications not configured.  Missing SecretAPIKey.")
		return false
	}

	return true
}

func (me *PushyNotificationServer) SendNotification(msg *PushNotification) PushResponse {

	if !me.PushyPushSettings.Enable {
		return NewOkPushResponse()
	}

	pushType := msg.Type
	data := map[string]interface{}{
		"ack_id":         msg.AckID,
		"type":           pushType,
		"version":        msg.Version,
		"channel_id":     msg.ChannelID,
		"is_crt_enabled": msg.IsCRTEnabled,
		"server_id":      msg.ServerID,
	}

	if msg.Badge != -1 {
		data["badge"] = msg.Badge
	}

	if msg.RootID != "" {
		data["root_id"] = msg.RootID
	}

	if msg.IsIDLoaded {
		data["post_id"] = msg.PostID
		data["message"] = msg.Message
		data["id_loaded"] = true
		data["sender_id"] = msg.SenderID
		data["sender_name"] = "Someone"
		data["team_id"] = msg.TeamID
	} else if pushType == PushTypeMessage || pushType == PushTypeSession {
		data["team_id"] = msg.TeamID
		data["sender_id"] = msg.SenderID
		data["sender_name"] = msg.SenderName
		data["message"] = emoji.Sprint(msg.Message)
		data["channel_name"] = msg.ChannelName
		data["post_id"] = msg.PostID
		data["override_username"] = msg.OverrideUsername
		data["override_icon_url"] = msg.OverrideIconURL
		data["from_webhook"] = msg.FromWebhook
	}

	pl := PushyPayload{
		DeviceId: msg.DeviceID,
		Body:     data,
	}

	plJson, err := json.Marshal(pl)
	if err != nil {
		me.logger.Errorf("Failed to convert to json. msg=%v", msg)
		return NewErrorPushResponse("failed to encode")
	}

	me.logger.Infof("Sending pushy push notification for device=%v type=%v ackId=%v", me.PushyPushSettings.ReplaceForType, msg.Type, msg.AckID)

	err = me.sendAndRetry(plJson, 2)
	if err != nil {
		me.logger.Errorf("Failed to send to pushy. err=%v", err)
		return NewErrorPushResponse("failed to send")
	}

	return NewOkPushResponse()
}

func (me *PushyNotificationServer) sendAndRetry(data []byte, times int) error {
	for time := 1; time < times; time++ {
		authKey := me.PushyPushSettings.SecretAPIKey

		resp, err := me.Client.Post(fmt.Sprintf("https://api.pushy.me/push?api_key=%v", authKey), "application/json", bytes.NewReader(data))
		if err != nil {
			me.logger.Errorf("Failed to send to pushy. time=%v, err=%v", time, err)
			continue
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)

		var r PushyResponse
		json.Unmarshal(body, &r)
		if !r.Succ {
			me.logger.Errorf("Failed to send to pushy( by response ). time=%v", time)
			continue
		}

		return nil

	}

	return fmt.Errorf("all retry times are used, error remains.")
}
