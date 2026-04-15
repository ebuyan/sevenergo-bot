package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/kelseyhightower/envconfig"
)

const (
	checkURL     = "https://sevenergosbyt.ru/flk/check.php"
	homeURL      = "https://sevenergosbyt.ru/flk/home/"
	resultURL    = "https://sevenergosbyt.ru/flk/home/result.php"
	vtbPayURL    = "https://sevenergosbyt.ru/flk/home/vtb_pay.php"
	indexReferer = "https://sevenergosbyt.ru/flk/index.php"
	homeReferer  = "https://sevenergosbyt.ru/flk/home/"
)

// --- Конфиг ---

type config struct {
	Username     string `envconfig:"ENERGO_USERNAME"      required:"true"`
	LicevoySchet string `envconfig:"ENERGO_LICEVOY_SCHET" required:"true"`
	NomerSchet   string `envconfig:"ENERGO_NOMER_SCHET"   required:"true"`
	Key          string `envconfig:"ENERGO_KEY"           required:"true"`
	Email        string `envconfig:"ENERGO_EMAIL"         required:"true"`
	ListenAddr   string `envconfig:"LISTEN_ADDR"          default:":8080"`
}

var cfg config

func loadConfig() error {
	return envconfig.Process("", &cfg)
}

// --- Регексы ---

var reBalanceType = regexp.MustCompile(`(Переплата|Задолженность)\s+на\s+(\d{2}\.\d{2}\.\d{4})`)
var reAmount = regexp.MustCompile(`(\d+\.\d+)\s+руб\.`)
var reLastReading = regexp.MustCompile(`<strong>([\d.]+)</strong>&nbsp;\s*от\s*<strong>(\d{2}\.\d{2}\.\d{4})</strong>`)
var rePendingReading = regexp.MustCompile(`<strong>([\d.]+)<span[^>]*>\s*\*\s*</span></strong>\s*от\s*<strong>(\d{2}\.\d{2}\.\d{4})</strong>`)
var rePendingStatus = regexp.MustCompile(`<span[^>]*>(показания[^<]+)</span>`)
var reDiff = regexp.MustCompile(`Разница\s+(\d+)\s+кВт`)

// --- Типы ---

type homeResponse struct {
	Dept            float64  `json:"dept"`
	Date            string   `json:"date"`
	LastReading     float64  `json:"last_reading"`
	LastReadingDate string   `json:"last_reading_date"`
	PendingReading  *float64 `json:"pending_reading,omitempty"`
	PendingDate     string   `json:"pending_date,omitempty"`
	PendingStatus   string   `json:"pending_status,omitempty"`
	DiffKwh         *int     `json:"diff_kwh,omitempty"`
}

// --- Парсинг ---

func parseHome(html string) (homeResponse, error) {
	var resp homeResponse

	mType := reBalanceType.FindStringSubmatch(html)
	if mType == nil {
		return resp, fmt.Errorf("не найдена строка Переплата/Задолженность")
	}
	resp.Date = mType[2]

	mAmt := reAmount.FindStringSubmatch(html)
	if mAmt == nil {
		return resp, fmt.Errorf("не найдена сумма в руб.")
	}
	amount, err := strconv.ParseFloat(mAmt[1], 64)
	if err != nil {
		return resp, fmt.Errorf("парсинг суммы %q: %w", mAmt[1], err)
	}
	if mType[1] == "Переплата" {
		resp.Dept = -amount
	} else {
		resp.Dept = amount
	}

	if mLast := reLastReading.FindStringSubmatch(html); mLast != nil {
		resp.LastReading, _ = strconv.ParseFloat(mLast[1], 64)
		resp.LastReadingDate = mLast[2]
	}

	if mPend := rePendingReading.FindStringSubmatch(html); mPend != nil {
		val, _ := strconv.ParseFloat(mPend[1], 64)
		resp.PendingReading = &val
		resp.PendingDate = mPend[2]

		if mStatus := rePendingStatus.FindStringSubmatch(html); mStatus != nil {
			resp.PendingStatus = strings.TrimSpace(mStatus[1])
		}
		if mDiff := reDiff.FindStringSubmatch(html); mDiff != nil {
			d, _ := strconv.Atoi(mDiff[1])
			resp.DiffKwh = &d
		}
	}

	return resp, nil
}

// --- Сессия ---

func authenticate() (*http.Client, error) {
	jar, _ := cookiejar.New(nil)
	siteURL, _ := url.Parse("https://sevenergosbyt.ru")
	jar.SetCookies(siteURL, []*http.Cookie{
		{Name: "message_warning", Value: "message"},
		{Name: "pum-1613", Value: "true"},
	})

	client := &http.Client{Jar: jar}

	form := url.Values{
		"username":     {cfg.Username},
		"LicevoySchet": {cfg.LicevoySchet},
		"NomerSchet":   {cfg.NomerSchet},
		"key":          {cfg.Key},
		"remember-me":  {"remember-me"},
		"act":          {"login"},
	}

	req, err := http.NewRequest(http.MethodPost, checkURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build check request: %w", err)
	}
	setHeaders(req, indexReferer, "application/x-www-form-urlencoded; charset=UTF-8")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("check.php: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("check.php read body: %w", err)
	}
	log.Printf("[check.php] status=%d body=%s", resp.StatusCode, string(body))

	var checkResp struct {
		Status  string `json:"status"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &checkResp); err != nil {
		return nil, fmt.Errorf("check.php unexpected response: %s", string(body))
	}
	if checkResp.Status != "ok" {
		msg := strings.ReplaceAll(checkResp.Message, "<br>", " ")
		return nil, fmt.Errorf("%s", strings.TrimSpace(msg))
	}

	return client, nil
}

func fetchHome(client *http.Client) (homeResponse, error) {
	req, err := http.NewRequest(http.MethodGet, homeURL, nil)
	if err != nil {
		return homeResponse{}, fmt.Errorf("build home request: %w", err)
	}
	setHeaders(req, indexReferer, "")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")

	resp, err := client.Do(req)
	if err != nil {
		return homeResponse{}, fmt.Errorf("home/: %w", err)
	}
	defer resp.Body.Close()
	log.Printf("[home/] status=%d final_url=%s", resp.StatusCode, resp.Request.URL)

	htmlBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return homeResponse{}, fmt.Errorf("read home/ body: %w", err)
	}

	return parseHome(string(htmlBytes))
}

// --- Хендлеры ---

// GET /status — баланс и показания счётчика
func statusHandler(w http.ResponseWriter, r *http.Request) {
	client, err := authenticate()
	if err != nil {
		writeError(w, http.StatusBadGateway, "auth: "+err.Error())
		return
	}

	result, err := fetchHome(client)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, result)
}

// GET /submit?value=88500 — передать показания счётчика
func submitHandler(w http.ResponseWriter, r *http.Request) {
	value := r.URL.Query().Get("value")
	if value == "" {
		writeError(w, http.StatusBadRequest, "параметр value обязателен (текущие показания счётчика)")
		return
	}
	newReading, err := strconv.ParseFloat(value, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "value должен быть числом")
		return
	}

	client, err := authenticate()
	if err != nil {
		writeError(w, http.StatusBadGateway, "auth: "+err.Error())
		return
	}

	current, err := fetchHome(client)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "получение текущих показаний: "+err.Error())
		return
	}

	baseline := current.LastReading
	baselineLabel := fmt.Sprintf("зафиксированным %.0f", current.LastReading)
	if current.PendingReading != nil {
		baseline = *current.PendingReading
		baselineLabel = fmt.Sprintf("показаниям на обработке %.0f", *current.PendingReading)
	}

	if newReading <= baseline {
		writeError(w, http.StatusBadRequest, fmt.Sprintf(
			"показания %.0f не могут быть меньше или равны %s", newReading, baselineLabel,
		))
		return
	}

	if diff := newReading - current.LastReading; diff > 4000 {
		writeError(w, http.StatusBadRequest, fmt.Sprintf(
			"разница %.0f кВт·ч превышает допустимые 4000 кВт·ч (зафиксировано: %.0f)",
			diff, current.LastReading,
		))
		return
	}

	target, _ := url.Parse(resultURL)
	target.RawQuery = url.Values{
		"TOTALZONES":       {"1"},
		"NewCounterZone_1": {value},
	}.Encode()

	req, err := http.NewRequest(http.MethodGet, target.String(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "build result request: "+err.Error())
		return
	}
	setHeaders(req, homeReferer, "")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Sec-Fetch-User", "?1")
	req.Header.Set("Upgrade-Insecure-Requests", "1")

	resultResp, err := client.Do(req)
	if err != nil {
		writeError(w, http.StatusBadGateway, "result.php: "+err.Error())
		return
	}
	if err := resultResp.Body.Close(); err != nil {
		log.Printf("[result.php] close body: %v", err)
	}
	log.Printf("[result.php] status=%d final_url=%s", resultResp.StatusCode, resultResp.Request.URL)

	result, err := fetchHome(client)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "parse home after submit: "+err.Error())
		return
	}

	if result.PendingReading == nil || *result.PendingReading != newReading {
		pending := "нет"
		if result.PendingReading != nil {
			pending = fmt.Sprintf("%.0f", *result.PendingReading)
		}
		writeError(w, http.StatusInternalServerError, fmt.Sprintf(
			"показания не применились: ожидалось %.0f на обработке, получено %s", newReading, pending,
		))
		return
	}

	writeJSON(w, result)
}

// GET /pay?amount=100&email=user@mail.ru — получить ссылку на оплату ВТБ
func payHandler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	amount := q.Get("amount")
	if amount == "" {
		writeError(w, http.StatusBadRequest, "параметр amount обязателен (сумма платежа)")
		return
	}
	if _, err := strconv.ParseFloat(amount, 64); err != nil {
		writeError(w, http.StatusBadRequest, "amount должен быть числом")
		return
	}

	client, err := authenticate()
	if err != nil {
		writeError(w, http.StatusBadGateway, "auth: "+err.Error())
		return
	}

	form := url.Values{
		"amount":         {amount},
		"uemail":         {cfg.Email},
		"optype":         {"ee"},
		"scr_width":      {""},
		"scr_height":     {""},
		"scr_colorDepth": {""},
		"tzOffset":       {""},
		"javaEnabled":    {""},
		"deviceId":       {""},
	}

	req, err := http.NewRequest(http.MethodPost, vtbPayURL, strings.NewReader(form.Encode()))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "build vtb_pay request: "+err.Error())
		return
	}
	setHeaders(req, homeReferer, "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Sec-Fetch-User", "?1")
	req.Header.Set("Upgrade-Insecure-Requests", "1")

	noRedirectClient := *client
	noRedirectClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}

	resp, err := noRedirectClient.Do(req)
	if err != nil {
		writeError(w, http.StatusBadGateway, "vtb_pay.php: "+err.Error())
		return
	}
	defer resp.Body.Close()
	log.Printf("[vtb_pay.php] status=%d", resp.StatusCode)

	redirectURL := resp.Header.Get("Location")
	if redirectURL == "" {
		writeError(w, http.StatusBadGateway, fmt.Sprintf(
			"vtb_pay.php не вернул редирект (status=%d)", resp.StatusCode,
		))
		return
	}

	writeJSON(w, map[string]string{"redirect_url": redirectURL})
}

// --- Утилиты ---

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("[json] encode error: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(map[string]string{"error": msg}); err != nil {
		log.Printf("[json] encode error: %v", err)
	}
}

func setHeaders(req *http.Request, referer, contentType string) {
	req.Header.Set("Accept-Language", "ru-RU,ru;q=0.9,en-US;q=0.8,en;q=0.7")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Origin", "https://sevenergosbyt.ru")
	req.Header.Set("Pragma", "no-cache")
	req.Header.Set("Referer", referer)
	req.Header.Set("Sec-Ch-Ua", `"Chromium";v="146", "Not-A.Brand";v="24", "Google Chrome";v="146"`)
	req.Header.Set("Sec-Ch-Ua-Mobile", "?0")
	req.Header.Set("Sec-Ch-Ua-Platform", "macOS")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/146.0.0.0 Safari/537.36")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
}

func main() {
	if err := loadConfig(); err != nil {
		log.Fatalf("конфиг: %v", err)
	}

	http.HandleFunc("/status", statusHandler)
	http.HandleFunc("/submit", submitHandler)
	http.HandleFunc("/pay", payHandler)

	fmt.Println("Сервер: http://localhost" + cfg.ListenAddr)
	fmt.Println("  GET /status")
	fmt.Println("  GET /submit?value=88500")
	fmt.Println("  GET /pay?amount=100")

	log.Fatal(http.ListenAndServe(cfg.ListenAddr, nil))
}
