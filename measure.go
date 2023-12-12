package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/finfinack/measure/data"

	"github.com/gin-gonic/gin"
	"github.com/golang/glog"
	"github.com/gorilla/websocket"
)

var (
	port    = flag.Int("port", 8080, "Listening port for webserver.")
	tlsCert = flag.String("tlsCert", "", "Path to TLS Certificate. If this and -tlsKey is specified, service runs as TLS server.")
	tlsKey  = flag.String("tlsKey", "", "Path to TLS Key. If this and -tlsCert is specified, service runs as TLS server.")
)

const (
	wsEndpoint      = "/measure/v1/ws"
	collectEndpoint = "/measure/v1/collect"
)

var (
	upgrader = websocket.Upgrader{} // use default option

	status = map[string]json.RawMessage{}
)

type MeasureServer struct {
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
			status[msg.Src] = json.RawMessage(message)
		default:
			continue
		}
	}
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
		d, ok := status[parsedQueryParameters.Device]
		if !ok {
			ctx.JSON(http.StatusNotFound, gin.H{})
		}
		ctx.JSON(http.StatusOK, gin.H{
			"status": d,
		})
	default:
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

	gin.SetMode(gin.ReleaseMode)
	router := gin.Default()
	router.SetFuncMap(template.FuncMap{})

	srv := MeasureServer{
		Server: &http.Server{
			Addr:    fmt.Sprintf(":%d", *port),
			Handler: router, // use `http.DefaultServeMux`
		},
	}
	router.GET(wsEndpoint, srv.wsHandler)
	router.GET(collectEndpoint, srv.collectHandler)

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
