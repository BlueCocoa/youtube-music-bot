package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/go-telegram-bot-api/telegram-bot-api"
	log "github.com/sirupsen/logrus"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

type Config struct {
	Token string `json:"token"`
	MusicDir string `json:"music_dir"`
	MaxFileSize int64 `json:"max_filesize"`
	Python3 string `json:"python3"`
	LogLevel string `json:"log_level"`
}

var config Config
func init() {
	confPtr := flag.String("conf", "config.json", "Path to the config file")
	configFile, err := os.Open(*confPtr)
	defer configFile.Close()
	if err != nil {
		log.Fatal(err)
		os.Exit(1)
	}
	jsonParser := json.NewDecoder(configFile)
	err = jsonParser.Decode(&config)
	if err != nil {
		log.Fatal(err)
		os.Exit(1)
	}

	log.SetOutput(os.Stdout)
	log.SetLevel(log.InfoLevel)
	logLevel := strings.ToLower(config.LogLevel)
	switch logLevel {
	case "debug":
		log.SetLevel(log.DebugLevel)
	case "info":
		log.SetLevel(log.InfoLevel)
	case "warning":
		log.SetLevel(log.WarnLevel)
	case "error":
		log.SetLevel(log.ErrorLevel)
	case "fatal":
		log.SetLevel(log.FatalLevel)
	default:
		log.Errorf("Unknown log level '%s', will set to Info level", logLevel)
		log.SetLevel(log.InfoLevel)
	}
	log.Debugln("config:", config)
}

func replyError(bot *tgbotapi.BotAPI, update tgbotapi.Update, errMsg string, err error) {
	msg := tgbotapi.NewMessage(update.Message.Chat.ID, errMsg)
	msg.ReplyToMessageID = update.Message.ReplyToMessage.MessageID
	bot.Send(msg)
	log.Errorf("[%d] %s: %s: %v", update.Message.Chat.ID, update.Message.From.UserName, errMsg, err)
}

func downloadFile(URL, userAgent, fileName string) error {
	//Get the response bytes from the url
	client := &http.Client{}
	req, err := http.NewRequest("GET", URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode != 200 {
		return errors.New("Received non 200 response code: " + string(response.StatusCode))
	}

	file, err := os.Create(fileName)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = io.Copy(file, response.Body)
	if err != nil {
		return err
	}

	return nil
}

func main() {
	bot, err := tgbotapi.NewBotAPI(config.Token)
	if err != nil {
		log.Fatal(err)
		os.Exit(1)
	}
	log.Infof("Authorized on account %s", bot.Self.UserName)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates, err := bot.GetUpdatesChan(u)
	r := regexp.MustCompile("^(https://)?www\\.youtube\\.com/watch\\?v=([^\\s]+)$")
	err = os.MkdirAll(config.MusicDir, os.ModePerm)
	if err != nil {
		log.Fatal(err)
		os.Exit(1)
	}

	for update := range updates {
		if update.Message == nil {
			continue
		}

		text := strings.TrimSpace(update.Message.Text)
		log.Infof("[%s] %s", update.Message.From.UserName, text)

		var msg tgbotapi.MessageConfig
		if r.MatchString(text) {
			replyMessageID := update.Message.MessageID
			msg = tgbotapi.NewMessage(update.Message.Chat.ID, "OK! Music downloading...")
			bot.Send(msg)
			log.Infof("Download music: %s", text)

			go func() {
				args := []string{"-m", "youtube_dl", "--dump-json", "-f", "bestaudio[ext=m4a]", "-s", text}
				cmd := exec.Command(config.Python3, args...)
				output, err := cmd.Output()
				if err != nil {
					replyError(bot, update, "Sorry, there is something wrong at my side. Please try again later QwQ", err)
				} else {
					jsonOutput := make(map[string]interface{})
					err = json.Unmarshal(output, &jsonOutput)
					if err != nil {
						replyError(bot, update, "Sorry, there is something wrong at my side while parsing JSON response. Please try again later QwQ", err)
					} else {
						if _, ok := jsonOutput["url"]; ok {
							videoID := jsonOutput["id"].(string)
							ext := jsonOutput["ext"].(string)
							URL := jsonOutput["url"].(string)
							filesize := (int64)(jsonOutput["filesize"].(float64))
							filename := fmt.Sprintf("%s/%s.%s", config.MusicDir, videoID, ext)
							if info, err := os.Stat(filename); err == nil {
								if filesize == info.Size() {
									audioMsg := tgbotapi.NewAudioUpload(update.Message.Chat.ID, filename)
									audioMsg.ReplyToMessageID = replyMessageID
									bot.Send(audioMsg)
									return
								}
							}

							if filesize > config.MaxFileSize {
								replyError(bot, update, "Sorry, the audio file size is larger than 32MB.", nil)
							} else {
								httpHeaders := jsonOutput["http_headers"].(map[string]interface{})
								err := downloadFile(URL, httpHeaders["User-Agent"].(string), filename)
								if err != nil {
									replyError(bot, update, "Sorry, cannot download the requested music file now. Please try again later QwQ", err)
								} else {
									audioMsg := tgbotapi.NewAudioUpload(update.Message.Chat.ID, filename)
									audioMsg.ReplyToMessageID = replyMessageID
									bot.Send(audioMsg)
								}
							}
						} else {
							replyError(bot, update, "Sorry, the JSON response is malformatted. Please try again later QwQ", err)
						}
					}
				}
			}()
		} else {
			replyError(bot, update, "Sorry, I can only handle single YouTube URL at a time", nil)
		}
	}
}
