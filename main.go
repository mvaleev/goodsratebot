package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"image/jpeg"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/viper"
	"gopkg.in/bieber/barcode.v0"
	"gopkg.in/telegram-bot-api.v4"
)

type (
	msgResponse struct {
		chatID    int64
		messageID int
		name      string
		bc        string
		r         raiting
		count     int64
	}
	msgRequest struct {
		chatID    int64
		messageID int
		text      string
	}
	bcJSON struct {
		Status int      `json:"status"`
		Names  []string `json:"names"`
	}
	raiting struct {
		One   string `redis:"one"`
		Two   string `redis:"two"`
		Three string `redis:"three"`
		Four  string `redis:"four"`
		Five  string `redis:"five"`
	}
)

var (
	configFile string
	myClient   = &http.Client{Timeout: 2 * time.Second}
	chnResp    = make(chan msgResponse, 5)
	chnReq     = make(chan msgRequest, 5)
	star       = "\xE2\xAD\x90"
	message    = "\n\nРейтинг:\n" + star + star + star + star + star + " = %v\n" +
		star + star + star + star + " = %v\n" + star + star + star + " = %v\n" +
		star + star + " = %v\n" + star + " = %v\n" +
		"голосов: %v\n"
)

func init() {
	flag.StringVar(&configFile, "configFile", "config.yml", "configuration file")
	flag.Parse()

	viper.SetConfigType("yaml")
	viper.SetConfigFile(configFile)
	err := viper.ReadInConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nError: %s \n\n", err)
		fmt.Fprintf(os.Stderr, "Usage of %s:\n", os.Args[0])
		flag.Usage()
		os.Exit(1)
	}
}

func main() {
	go getResp()

	botAPIKey := viper.GetString("TGAPIKey")
	bot, err := tgbotapi.NewBotAPI(botAPIKey)
	if err != nil {
		log.Panic(err)
	}

	bot.Debug = false

	log.Printf("Authorized on account %s", bot.Self.UserName)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates, _ := bot.GetUpdatesChan(u)

	for {
		select {
		case u := <-updates:

			if u.Message != nil {
				chatID := u.Message.Chat.ID
				msgID := u.Message.MessageID
				defResp := "Надо отправить фото"

				if u.Message.Photo != nil {
					fileID := getMaxFileID(*u.Message.Photo)
					photoURL, err := bot.GetFileDirectURL(fileID)
					if err != nil {
						log.Fatalf("u.Message: Load Photo File from API fail: %s", err)
					}
					chnReq <- msgRequest{chatID, msgID, photoURL}
				} else {
					msg := tgbotapi.NewMessage(chatID, defResp)
					bot.Send(msg)
				}
			}

			if u.CallbackQuery != nil {
				chatID := u.CallbackQuery.Message.Chat.ID
				msgID := u.CallbackQuery.Message.MessageID
				userID := strconv.Itoa(u.CallbackQuery.From.ID)

				cbData := strings.Split(u.CallbackQuery.Data, ":")

				currentUserRaiting, err := getBarcodeRaitingUser("rating:"+cbData[1]+":users", userID)
				if err != nil {
					log.Printf("u.CallbackQuery: Error currentUserRaiting for %v: %s", userID, err)
				}

				if string(currentUserRaiting) != cbData[0] && currentUserRaiting != nil {
					err = incBarcodeRaiting("rating:"+cbData[1], string(currentUserRaiting), []byte("-1"))
					if err != nil {
						log.Printf("u.CallbackQuery: Error incBarcodeRaiting (-1): %s", err)
					}
				}

				err = setBarcodeRaiting("rating:"+cbData[1]+":users", userID, []byte(cbData[0]))
				if err != nil {
					log.Printf("u.CallbackQuery: Error setBarcodeRaiting: %s", err)
				}
				err = incBarcodeRaiting("rating:"+cbData[1], cbData[0], []byte("1"))
				if err != nil {
					log.Printf("u.CallbackQuery: Error incBarcodeRaiting (+1): %s", err)
				}
				r, err := getBarcodeRaiting("rating:" + cbData[1])
				if err != nil {
					log.Printf("u.CallbackQuery: Error getBarcodeRaiting for bc %v: %s", cbData[1], err)
				}
				name, _ := getBarcodeName("name:" + cbData[1])
				count, _ := getBarcodeRaitingLen("rating:" + cbData[1] + ":users")
				text := responseMessage(string(name), r, count)

				msg := tgbotapi.NewEditMessageText(chatID, msgID, text)

				bot.Send(msg)
			}

		case resp := <-chnResp:
			if resp.name != "" {
				text := responseMessage(resp.name, resp.r, resp.count)
				msg := tgbotapi.NewMessage(resp.chatID, text)

				r1 := tgbotapi.NewInlineKeyboardButtonData(star, "one:"+resp.bc)
				r2 := tgbotapi.NewInlineKeyboardButtonData(star, "two:"+resp.bc)
				r3 := tgbotapi.NewInlineKeyboardButtonData(star, "three:"+resp.bc)
				r4 := tgbotapi.NewInlineKeyboardButtonData(star, "four:"+resp.bc)
				r5 := tgbotapi.NewInlineKeyboardButtonData(star, "five:"+resp.bc)
				k := tgbotapi.NewInlineKeyboardRow(r1, r2, r3, r4, r5)

				msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(k)
				bot.Send(msg)
			} else {
				msg := tgbotapi.NewMessage(resp.chatID, "Товар не найден")
				bot.Send(msg)
			}
		}
	}
}

func getResp() {
	// goroutine for magic
	for msg := range chnReq {
		var bc, resp string
		var r raiting
		var count int64

		// get image from telegram and search barcode
		response, err := http.Get(msg.text)
		if err != nil {
			log.Panic(err)
		}
		defer response.Body.Close()

		src, _ := jpeg.Decode(response.Body)
		img := barcode.NewImage(src)
		scanner := barcode.NewScanner().
			SetEnabledAll(true)

		symbols, _ := scanner.ScanImage(img)
		for _, s := range symbols {
			bc = s.Data
		}

		if bc != "" {
			// get barcode name from redis
			if name, err := getBarcodeName("name:" + bc); err == nil {
				r, err = getBarcodeRaiting("rating:" + bc)
				if err != nil {
					log.Printf("getResp: Error getBarcodeRaiting for bc %v: %s", bc, err)
				}
				count, err = getBarcodeRaitingLen("rating:" + bc + ":users")
				if err != nil {
					log.Printf("getResp: Error getBarcodeRaitingLen for bc %v: %s", bc, err)
				}
				resp = string(name)
			} else {
				// get barcode name from remote API
				status, name := getNameFromBarcode(bc)
				if status {
					log.Println("getResp: Get name from remote API")
					r, err = getBarcodeRaiting("rating:" + bc)
					if err != nil {
						log.Printf("getResp: Error getBarcodeRaiting for bc %v: %s", bc, err)
					}
					count, err = getBarcodeRaitingLen("rating:" + bc + ":users")
					if err != nil {
						log.Printf("getResp: Error getBarcodeRaitingLen for bc %v: %s", bc, err)
					}
					resp = string(name)
					// write barcode name to redis
					err = setBarcodeName("name:"+bc, []byte(name))
					if err != nil {
						log.Printf("getResp: Error setBarcodeName for bc %v: %s", bc, err)
					}
				}
			}
		}
		chnResp <- msgResponse{msg.chatID, msg.messageID, resp, bc, r, count}
	}
}

// generate message
func responseMessage(name string, r raiting, count int64) string {
	if r.One == "" {
		r.One = "0"
	}
	if r.Two == "" {
		r.Two = "0"
	}
	if r.Three == "" {
		r.Three = "0"
	}
	if r.Four == "" {
		r.Four = "0"
	}
	if r.Five == "" {
		r.Five = "0"
	}
	c := strconv.FormatInt(count, 10)

	msg := fmt.Sprintf(message, r.Five, r.Four, r.Three, r.Two, r.One, c)
	return name + msg
}

// get best with quality image
func getMaxFileID(photos []tgbotapi.PhotoSize) string {
	result := photos[0].FileID
	width := photos[0].Width

	for _, photo := range photos {
		if width < photo.Width && photo.Width <= 4096 {
			result = photo.FileID
			width = photo.Width
		}
	}
	return result
}

// get name from API
func getNameFromBarcode(bcd string) (bool, string) {
	apiURL := viper.GetString("apiURL")
	bc := bcJSON{}

	err := getJSON(apiURL+bcd, &bc)
	if err != nil {
		log.Printf("getNameFromBarcode: Error getJSON for bcd %v: %s", bcd, err)
		return false, ""
	}

	if bc.Status == 200 {
		return true, bc.Names[0]
	}
	return false, ""
}

// get JSON from URL
func getJSON(url string, target interface{}) error {
	r, err := myClient.Get(url)
	if err != nil {
		return err
	}
	defer r.Body.Close()

	return json.NewDecoder(r.Body).Decode(target)
}
