package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/finfinack/measure/data"

	"github.com/gin-gonic/gin"
	"github.com/golang/glog"
	"github.com/gorilla/websocket"
	ttlcache "github.com/jellydator/ttlcache/v2"
)

var (
	port     = flag.Int("port", 8080, "Listening port for webserver.")
	tlsCert  = flag.String("tlsCert", "", "Path to TLS Certificate. If this and -tlsKey is specified, service runs as TLS server.")
	tlsKey   = flag.String("tlsKey", "", "Path to TLS Key. If this and -tlsCert is specified, service runs as TLS server.")
	cacheTTL = flag.Duration("cacheTTL", 3*time.Hour, "Duration for which to keep the entries in cache.")
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
}

func (m *MeasureServer) wsHandler(ctx *gin.Context) {
	w, r := ctx.Writer, ctx.Request
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		glog.Warningf("upgrade: %s", err)
		return
	}
	defer c.Close()

	for {
		_, message, err := c.ReadMessage()
		if err != nil {
			glog.Warningf("read: %s", err)
			break
		}

		glog.V(4).Infof("recv: %s", message)
		var msg data.WSMessage
		if err := json.Unmarshal(message, &msg); err != nil {
			glog.Warningf("unmarshal failed: %s", err)
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
		Device      string `form:"dev"`
		Temperature string `form:"temp"`
		Humidity    string `form:"hum"`
	}

	var parsedQueryParameters queryParameters
	if err := ctx.ShouldBind(&parsedQueryParameters); err != nil {
		ctx.AbortWithError(http.StatusBadRequest, err)
		return
	}

	r := data.ReportStatus{
		Device:      parsedQueryParameters.Device,
		Temperature: parsedQueryParameters.Temperature,
		Humidity:    parsedQueryParameters.Humidity,
	}
	if r.Device == "" || (r.Temperature == "" && r.Humidity == "") {
		ctx.AbortWithError(http.StatusBadRequest, errors.New("not enough parameters set"))
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
	ctx := context.Background()
	// Set defaults for glog flags. Can be overridden via cmdline.
	flag.Set("logtostderr", "true")
	flag.Set("stderrthreshold", "WARNING")
	flag.Set("v", "1")
	// Parse flags globally.
	flag.Parse()

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
	}
	router.GET(wsEndpoint, srv.wsHandler)
	router.GET(collectEndpoint, srv.collectHandler)
	router.GET(reportEndpoint, srv.reportHandler)

	if *tlsCert != "" && *tlsKey != "" {
		router.RunTLS(fmt.Sprintf(":%d", *port), *tlsCert, *tlsKey)
	} else {
		router.Run(fmt.Sprintf(":%d", *port))
	}

	// Wait for abort signal (e.g. CTRL-C pressed).
	c := make(chan os.Signal)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		srv.Server.Shutdown(ctx)
		glog.Flush()

		os.Exit(1)
	}()
}
