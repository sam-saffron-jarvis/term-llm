package serve

import (
	"net/http"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/term-llm/term-llm/internal/providerhttp"
)

func newTelegramBotAPI(token string) (*tgbotapi.BotAPI, error) {
	base, err := providerhttp.EnvBaseURL("TELEGRAM_BASE_URL", "https://api.telegram.org")
	if err != nil {
		return nil, err
	}
	return tgbotapi.NewBotAPIWithClient(token, base+"/bot%s/%s", newTelegramBotHTTPClient(telegramBotAPIRequestTimeout, telegramBotAPILongPollTimeout))
}

func downloadTelegramFile(directURL string) (*http.Response, error) {
	base, err := providerhttp.EnvBaseURL("TELEGRAM_BASE_URL", "https://api.telegram.org")
	if err != nil {
		return nil, err
	}
	// telegram-bot-api's GetFileDirectURL synthesizes this fixed origin rather
	// than using its configured API endpoint. Rewrite ONLY that SDK route, not
	// arbitrary URLs supplied by another file getter or an API response.
	const sdkFilePrefix = "https://api.telegram.org/file/bot"
	if strings.HasPrefix(directURL, sdkFilePrefix) {
		directURL = base + "/file/bot" + strings.TrimPrefix(directURL, sdkFilePrefix)
	}
	return telegramDownloadHTTPClient.Get(directURL)
}
