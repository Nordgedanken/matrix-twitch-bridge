package asLogic

import (
	"fmt"
	"github.com/Nordgedanken/matrix-twitch-bridge/asLogic/db"
	"github.com/Nordgedanken/matrix-twitch-bridge/asLogic/queryHandler"
	"github.com/Nordgedanken/matrix-twitch-bridge/asLogic/twitch"
	"github.com/Nordgedanken/matrix-twitch-bridge/asLogic/twitch/login"
	"github.com/Nordgedanken/matrix-twitch-bridge/asLogic/user"
	"github.com/Nordgedanken/matrix-twitch-bridge/asLogic/util"
	"github.com/fatih/color"
	"github.com/gorilla/mux"
	"maunium.net/go/mautrix-appservice-go"
	"net/http"
	"os"
)

// Init starts the interactive Config generator and exits
func Init() {
	var boldGreen = color.New(color.FgGreen).Add(color.Bold)
	appservice.GenerateRegistration("twitch", "twitch", true, true)
	boldGreen.Println("Please restart the Twitch-Appservice with \"--client_id\"-flag applied")
}

func prepareRun() error {
	var err error

	util.Config, err = appservice.Load(util.CfgFile)
	if err != nil {
		return err
	}

	qHandler := queryHandler.QueryHandler()

	qHandler.TwitchRooms, err = db.GetTwitchRooms()
	if err != nil {
		return err
	}

	qHandler.Aliases, err = db.GetRooms()
	if err != nil {
		return err
	}

	qHandler.TwitchUsers, err = db.GetTwitchUsers()
	if err != nil {
		return err
	}

	qHandler.Users, err = db.GetASUsers()
	if err != nil {
		return err
	}

	qHandler.RealUsers, err = db.GetRealUsers()
	if err != nil {
		return err
	}

	util.BotUser, err = db.GetBotUser()
	if err != nil {
		return err
	}

	util.Config.Init(qHandler)

	r := mux.NewRouter()
	r.HandleFunc("/callback", login.Callback).Methods(http.MethodGet)

	go func() {
		var err error
		if len(util.TLSCert) == 0 || len(util.TLSKey) == 0 {
			err = fmt.Errorf("You need to have a SSL Cert!")
		} else {
			err = http.ListenAndServeTLS(util.Publicaddress, util.TLSCert, util.TLSKey, r)
		}
		if err != nil {
			util.Config.Log.Fatalln("Error while listening:", err)
			os.Exit(1)
		}
	}()

	util.Config.Listen()

	return nil
}

// Run starts the actual Appservice to let it listen to both ends
func Run() error {
	err := prepareRun()
	if err != nil {
		return err
	}

	util.BotUser.TwitchWS, err = twitch.Connect(util.BotUser.TwitchToken, util.BotUser.TwitchName)
	if err != nil {
		return err
	}

	twitch.Listen(queryHandler.QueryHandler().TwitchUsers, queryHandler.QueryHandler().TwitchRooms)

	for {
		select {
		case event := <-util.Config.Events:
			switch event.Type {
			case "m.room.message":
				qHandler := queryHandler.QueryHandler()
				mxUser := qHandler.RealUsers[event.SenderID]
				if mxUser == nil {
					mxUser = &user.RealUser{}
					mxUser.Mxid = event.SenderID
					db.SaveUser(mxUser, "REAL")
					login.SendLoginURL(mxUser)
					continue
				}
				if mxUser.TwitchWS == nil {
					if mxUser.TwitchTokenStruct.AccessToken != "" && mxUser.TwitchName != "" {
						mxUser.TwitchWS, err = twitch.Connect(mxUser.TwitchTokenStruct.AccessToken, mxUser.TwitchName)
						if err != nil {
							util.Config.Log.Errorln(err)
							continue
						}
					}
				}

			}
		}
	}
}
