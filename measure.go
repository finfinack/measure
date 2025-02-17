package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"net/http"
	"time"

	"github.com/finfinack/measure/data"

	"github.com/finfinack/logger/logging"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	ttlcache "github.com/jellydator/ttlcache/v2"
)

var (
	port     = flag.Int("port", 8080, "Listening port for webserver.")
	tlsCert  = flag.String("tlsCert", "", "Path to TLS Certificate. If this and -tlsKey is specified, service runs as TLS server.")
	tlsKey   = flag.String("tlsKey", "", "Path to TLS Key. If this and -tlsCert is specified, service runs as TLS server.")
	cacheTTL = flag.Duration("cacheTTL", 3*time.Hour, "Duration for which to keep the entries in cache.")
	logLevel = flag.String("loglevel", "INFO", "Log level to use.")
)

const (
	wsEndpoint      = "/measure/v1/ws"
	collectEndpoint = "/measure/v1/collect"
	reportEndpoint  = "/measure/v1/report"
)

var (
	upgrader = websocket.Upgrader{} // use default option
)

type MeasureServer struct {
	Cache  *ttlcache.Cache
	Server *http.Server
	Logger *logging.Logger
}

func (m *MeasureServer) wsHandler(ctx *gin.Context) {
	w, r := ctx.Writer, ctx.Request
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		m.Logger.Warnf("upgrade: %s", err)
		return
	}
	defer c.Close()

	for {
		_, message, err := c.ReadMessage()
		if err != nil {
			m.Logger.Warnf("read: %s", err)
			break
		}

		m.Logger.Debugf("recv: %s", message)
		var msg data.WSMessage
		if err := json.Unmarshal(message, &msg); err != nil {
			m.Logger.Warnf("unmarshal failed: %s", err)
			break
		}

		switch msg.Method {
		case data.MethodNotifyFullStatus:
			m.Cache.Set(msg.Src, json.RawMessage(message))
		default:
			continue
		}
	}
}

func (m *MeasureServer) reportHandler(ctx *gin.Context) {
	type queryParameters struct {
		ID          string `form:"id"`
		Temperature string `form:"temp"`
		Humidity    string `form:"hum"`
	}

	var parsedQueryParameters queryParameters
	if err := ctx.ShouldBind(&parsedQueryParameters); err != nil {
		ctx.AbortWithError(http.StatusBadRequest, err)
		return
	}

	r := data.ReportStatus{
		Device:      parsedQueryParameters.ID,
		Temperature: parsedQueryParameters.Temperature,
		Humidity:    parsedQueryParameters.Humidity,
	}
	if r.Device == "" || (r.Temperature == "" && r.Humidity == "") {
		ctx.AbortWithError(http.StatusBadRequest, errors.New("not enough parameters set"))
		return
	}
	msg, err := json.Marshal(r)
	if err != nil {
		ctx.AbortWithError(http.StatusBadRequest, err)
		return
	}
	m.Cache.Set(r.Device, json.RawMessage(msg))

	ctx.JSON(http.StatusOK, gin.H{})
}

func (m *MeasureServer) collectHandler(ctx *gin.Context) {
	type queryParameters struct {
		Device string `form:"device"`
	}

	var parsedQueryParameters queryParameters
	if err := ctx.ShouldBind(&parsedQueryParameters); err != nil {
		ctx.AbortWithError(http.StatusBadRequest, err)
		return
	}

	switch {
	case parsedQueryParameters.Device != "":
		s, err := m.Cache.Get(parsedQueryParameters.Device)
		if err != nil {
			ctx.AbortWithError(http.StatusNotFound, err)
			return
		}
		ctx.JSON(http.StatusOK, gin.H{
			"status": s.(json.RawMessage),
		})
	default:
		status := map[string]json.RawMessage{}
		for k, v := range m.Cache.GetItems() {
			status[k] = v.(json.RawMessage)
		}
		ctx.JSON(http.StatusOK, gin.H{
			"devices": status,
		})
	}
}

func main() {
	flag.Parse()

	// Set up logging
	log := logging.NewLogger("MAIN")
	lvl, err := logging.LevelToValue(*logLevel)
	if err != nil {
		log.Fatalf("Unable to map %q to a log level", *logLevel)
	}
	logging.SetMinLogLevel(lvl)
	defer log.Shutdown()

	cache := ttlcache.NewCache()
	cache.SetTTL(time.Duration(*cacheTTL))

	gin.SetMode(gin.ReleaseMode)
	router := gin.Default()
	router.SetFuncMap(template.FuncMap{})

	srv := MeasureServer{
		Cache: cache,
		Server: &http.Server{
			Addr:    fmt.Sprintf(":%d", *port),
			Handler: router, // use `http.DefaultServeMux`
		},
		Logger: logging.NewLogger("SERV"),
	}
	router.GET(wsEndpoint, srv.wsHandler)
	router.GET(collectEndpoint, srv.collectHandler)
	router.GET(reportEndpoint, srv.reportHandler)

	if *tlsCert != "" && *tlsKey != "" {
		router.RunTLS(fmt.Sprintf(":%d", *port), *tlsCert, *tlsKey)
	} else {
		router.Run(fmt.Sprintf(":%d", *port))
	}
}
