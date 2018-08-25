package asLogic

import (
	"fmt"
	dbImpl "github.com/Nordgedanken/matrix-twitch-bridge/asLogic/db/implementation"
	"github.com/Nordgedanken/matrix-twitch-bridge/asLogic/queryHandler"
	"github.com/Nordgedanken/matrix-twitch-bridge/asLogic/twitch/login"
	wsImpl "github.com/Nordgedanken/matrix-twitch-bridge/asLogic/twitch/websocket/implementation"
	"github.com/Nordgedanken/matrix-twitch-bridge/asLogic/user"
	"github.com/Nordgedanken/matrix-twitch-bridge/asLogic/util"
	"github.com/fatih/color"
	"github.com/gorilla/mux"
	"log"
	"maunium.net/go/gomatrix"
	"maunium.net/go/maulogger"
	"maunium.net/go/mautrix-appservice"
	"net/http"
	"os"
)

// Init starts the interactive Config generator and exits
func Init() {
	var boldGreen = color.New(color.FgGreen).Add(color.Bold)
	appservice.GenerateRegistration("twitch", "appservice-twitch", true, true)
	boldGreen.Println("Please restart the Twitch-Appservice with \"--client_id\"-flag applied")
}

func prepareRun() error {
	var err error

	util.Config, err = appservice.Load(util.CfgFile)
	if err != nil {
		return err
	}

	util.Config.Registration, err = appservice.LoadRegistration(util.Config.RegistrationPath)

	util.Config.Log = maulogger.Create()
	util.Config.LogConfig.Configure(util.Config.Log)
	util.Config.Log.Debugln("Logger initialized successfully.")

	util.DB = &dbImpl.DB{}

	util.Config.Log.Debugln("Creating queryHandler.")
	qHandler := queryHandler.QueryHandler()

	util.Config.Log.Debugln("Loading Twitch Rooms from DB.")
	qHandler.TwitchRooms, err = util.DB.GetTwitchRooms()
	if err != nil {
		return err
	}

	util.Config.Log.Debugln("Loading Rooms from DB.")
	qHandler.Aliases, err = util.DB.GetRooms()
	if err != nil {
		return err
	}

	util.Config.Log.Debugln("Loading Twitch Users from DB.")
	qHandler.TwitchUsers, err = util.DB.GetTwitchUsers()
	if err != nil {
		return err
	}

	util.Config.Log.Debugln("Loading AS Users from DB.")
	qHandler.Users, err = util.DB.GetASUsers()
	if err != nil {
		return err
	}

	util.Config.Log.Debugln("Loading Real Users from DB.")
	qHandler.RealUsers, err = util.DB.GetRealUsers()
	if err != nil {
		return err
	}

	util.Config.Log.Debugln("Loading Bot User from DB.")
	util.BotUser, err = util.DB.GetBotUser()
	if err != nil {
		return err
	}

	util.Config.Log.Infoln("Init...")
	_, err = util.Config.Init()
	if err != nil {
		log.Fatalln(err)
	}
	util.Config.QueryHandler = qHandler
	util.Config.Log.Infoln("Init Done...")

	util.Config.Log.Infoln("Starting public server...")
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

	return nil
}

// Run starts the actual Appservice to let it listen to both ends
func Run() error {
	err := prepareRun()
	if err != nil {
		return err
	}

	util.Config.Log.Debugln("Start Connecting BotUser to Twitch as: ", util.BotUser.TwitchName)

	util.Config.Log.Debugln("Start letting BotUser listen to Twitch")

	for _, v := range queryHandler.QueryHandler().Aliases {
		util.BotUser.Mux.Lock()
		v.TwitchWS = &wsImpl.WebsocketHolder{
			Done:        make(chan struct{}),
			TwitchRooms: queryHandler.QueryHandler().TwitchRooms,
			TwitchUsers: queryHandler.QueryHandler().TwitchUsers,
			RealUsers:   queryHandler.QueryHandler().RealUsers,
			Users:       queryHandler.QueryHandler().Users,
			TRoom:       v.TwitchChannel,
		}
		err = v.TwitchWS.Connect(util.BotUser.TwitchToken, util.BotUser.TwitchName)
		if err != nil {
			return err
		}

		v.TwitchWS.Listen()
		err = v.TwitchWS.Join(v.TwitchChannel)
		util.BotUser.Mux.Unlock()
		if err != nil {
			return err
		}
	}

	go func() {
		for {
			select {
			case event := <-util.Config.Events:
				util.Config.Log.Debugln("Got Event")
				switch event.Type {
				case "m.room.member":
					if event.Content.Membership == "join" {

						qHandler := queryHandler.QueryHandler()
						for _, v := range qHandler.Aliases {
							if v.ID == event.RoomID {
								if event.Sender != util.BotUser.MXClient.UserID {
									err = joinEventHandler(event)
									if err != nil {
										util.Config.Log.Errorln(err)
									}
								}
							}
						}
						continue

					}
				case "m.room.message":
					qHandler := queryHandler.QueryHandler()
					for _, v := range qHandler.Aliases {
						if v.ID == event.RoomID {
							if event.Sender != util.BotUser.MXClient.UserID {
								err = useEvent(event)
								if err != nil {
									util.Config.Log.Errorln(err)
								}
							}
						}
					}
					continue

				}
			}
		}
	}()

	util.Config.Log.Infoln("Starting Appservice Server...")
	util.Config.Start()

	select {}
}

func joinEventHandler(event *gomatrix.Event) error {
	qHandler := queryHandler.QueryHandler()
	mxUser := qHandler.RealUsers[event.Sender]
	asUser := qHandler.Users[event.Sender]
	util.Config.Log.Debugf("AS User: %+v\n", asUser)
	if asUser != nil || util.BotUser.Mxid == event.Sender {
		return nil
	}
	if mxUser == nil {
		util.Config.Log.Debugln("Creating new User")

		qHandler.RealUsers[event.Sender] = &user.RealUser{}
		mxUser := qHandler.RealUsers[event.Sender]
		mxUser.Mxid = event.Sender

		util.Config.Log.Debugln("Let new User Login")
		err := login.SendLoginURL(mxUser)
		if err != nil {
			return err
		}
		return nil
	}
	return nil
}

func useEvent(event *gomatrix.Event) error {
	qHandler := queryHandler.QueryHandler()
	mxUser := qHandler.RealUsers[event.Sender]
	asUser := qHandler.Users[event.Sender]
	util.Config.Log.Debugf("AS User: %+v\n", asUser)
	if asUser != nil || util.BotUser.Mxid == event.Sender || mxUser == nil {
		return nil
	}

	util.Config.Log.Infoln("Processing Event")

	util.Config.Log.Debugln("Check if Room of event is known")
	for _, v := range qHandler.Aliases {
		if v.ID == event.RoomID {

			util.Config.Log.Debugln("Check if we have already a open WS")
			if mxUser.TwitchWS == nil {
				util.Config.Log.Debugf("%+v\n", mxUser.TwitchTokenStruct)
				if mxUser.TwitchTokenStruct != nil && mxUser.TwitchTokenStruct.AccessToken != "" && mxUser.TwitchName != "" {
					var err error

					util.Config.Log.Debugln("Connect new WS to Twitch")
					v.TwitchWS = &wsImpl.WebsocketHolder{
						Done:        make(chan struct{}),
						TwitchRooms: queryHandler.QueryHandler().TwitchRooms,
						TwitchUsers: queryHandler.QueryHandler().TwitchUsers,
						RealUsers:   queryHandler.QueryHandler().RealUsers,
						Users:       queryHandler.QueryHandler().Users,
					}
					err = v.TwitchWS.Connect(mxUser.TwitchTokenStruct.AccessToken, mxUser.TwitchName)
					if err != nil {
						return err
					}
					v.TwitchWS.Listen()
				} else {
					return nil
				}
			}

			util.Config.Log.Debugln("Check if text or other Media")
			if event.Content.MsgType == "m.text" {
				util.Config.Log.Debugln("Send message to twitch")

				util.BotUser.Mux.Lock()
				err := v.TwitchWS.Send(v.TwitchChannel, event.Content.Body)
				util.BotUser.Mux.Unlock()
				if err != nil {
					return err
				}
			} else {
				util.Config.Log.Debugln("Send message to bridge Room to tell user to use plain text")
				resp, err := util.BotUser.MXClient.GetDisplayName(event.Sender)
				if err != nil {
					return err
				}
				_, err = util.BotUser.MXClient.SendNotice(event.RoomID, resp.DisplayName+": Please use Text only as Twitch doesn't support any other Media Format!")
				if err != nil {
					return err
				}
			}
		}
	}
	return nil
}
