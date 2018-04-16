package login

import (
	"context"
	"encoding/json"
	"github.com/Nordgedanken/matrix-twitch-bridge/asLogic/matrix_helper"
	"github.com/Nordgedanken/matrix-twitch-bridge/asLogic/queryHandler"
	twitch2 "github.com/Nordgedanken/matrix-twitch-bridge/asLogic/twitch"
	"github.com/Nordgedanken/matrix-twitch-bridge/asLogic/user"
	"github.com/Nordgedanken/matrix-twitch-bridge/asLogic/util"
	"github.com/matrix-org/gomatrix"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/twitch"
	"net/http"
	"time"
)

var conf *oauth2.Config

func SendLoginURL(ruser *user.RealUser) error {
	if conf == nil {
		conf = &oauth2.Config{
			ClientID:     util.ClientID,
			ClientSecret: util.ClientSecret,
			Scopes:       []string{"chat_login"},
			Endpoint:     twitch.Endpoint,
		}
	}
	// Redirect user to consent page to ask for permission
	// for the scopes specified above.
	url := conf.AuthCodeURL(ruser.Mxid, oauth2.AccessTypeOffline)

	if ruser.Room == "" {
		resp, err := matrix_helper.CreateRoom(util.BotUser.MXClient, "Twitch Bot", "", "", "trusted_private_chat")
		if err != nil {
			return err
		}
		ruser.Room = resp.RoomID
	}
	inviteReq := &gomatrix.ReqInviteUser{
		UserID: ruser.Mxid,
	}
	util.BotUser.MXClient.InviteUser(ruser.Room, inviteReq)
	util.BotUser.MXClient.SendNotice(ruser.Room, "Please Login to Twitch using the following URL: "+url+"\n You will get redirected to a Magic Page which you can close as soon as it loaded.")

	return nil
}

// Get Info about the just logged in User: https://api.twitch.tv/kraken/user?oauth_token=<token we got from the login>

type profile struct {
	DisplayName string      `json:"display_name"`
	ID          int         `json:"_id"`
	Name        string      `json:"name"`
	Type        string      `json:"type"`
	Bio         interface{} `json:"bio"`
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"`
	Logo        string      `json:"logo"`
	Links       struct {
		Self string `json:"self"`
	} `json:"_links"`
	Email         string `json:"email"`
	Partnered     bool   `json:"partnered"`
	Notifications struct {
		Push  bool `json:"push"`
		Email bool `json:"email"`
	} `json:"notifications"`
}

// Callback
func Callback(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()
	query := r.URL.Query()
	code, cok := query["code"]
	state, sok := query["state"]
	if sok && cok {
		tok, err := conf.Exchange(ctx, code[0])
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
		}
		queryHandler.QueryHandler().RealUsers[state[0]].TwitchTokenStruct = tok
		queryHandler.QueryHandler().RealUsers[state[0]].TwitchHTTPClient = conf.Client(ctx, tok)
		queryHandler.QueryHandler().RealUsers[state[0]].TwitchHTTPClient.Timeout = time.Second * 10

		var p profile

		resp, err := queryHandler.QueryHandler().RealUsers[state[0]].TwitchHTTPClient.Get("https://api.twitch.tv/kraken/user?oauth_token=" + tok.AccessToken)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
		}
		if resp.Body == nil {
			w.WriteHeader(http.StatusInternalServerError)
		}
		defer resp.Body.Close()

		err = json.NewDecoder(resp.Body).Decode(&p)
		queryHandler.QueryHandler().RealUsers[state[0]].TwitchWS, err = twitch2.Connect(tok.AccessToken, p.Name)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}
	w.WriteHeader(http.StatusOK)
}
