package main

import (
	"flag"
	"fmt"
	"html/template"
	"io/ioutil"
	"net/http"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/labstack/echo"
	"github.com/ngaut/log"
)

const (
	namespace = "default"
	tprName   = "tidbclusters"
)

var (
	kubeCli        kubernetes.Interface
	tprURL         string
	logLevel       string
	tikvConfigTmpl *template.Template
	pdConfigTmpl   *template.Template
)

func init() {
	flag.StringVar(&logLevel, "L", "info", "log level: debug, info, warn, error, fatal (default \"info\")")
	flag.Parse()
	log.SetLevelByString(logLevel)
	log.SetHighlighting(true)
	pdTmpl, err := ioutil.ReadFile("/pd.toml.tmpl")
	if err != nil {
		log.Fatal(err)
	}
	pdConfigTmpl = template.Must(template.New("pd-config").Parse(string(pdTmpl)))
	tikvTmpl, err := ioutil.ReadFile("/tikv.toml.tmpl")
	if err != nil {
		log.Fatal(err)
	}
	tikvConfigTmpl = template.Must(template.New("tikv-config").Parse(string(tikvTmpl)))
	cfg, err := rest.InClusterConfig()
	if err != nil {
		log.Fatal(err)
	}
	tprURL = fmt.Sprintf("%s/apis/pingcap.com/v1/namespaces/%s/tidbclusters", cfg.Host, namespace)
	kubeCli, err = kubernetes.NewForConfig(cfg)
	if err != nil {
		log.Fatal(err)
	}
}

// Response is RESTful response
type Response struct {
	Action     string     `json:"action"`
	StatusCode int        `json:"status_code"`
	Message    string     `json:"message,omitempty"`
	Payload    []*Cluster `json:"payload,omitempty"`
}

func main() {
	e := echo.New()
	// List clusters
	e.GET("/clusters", func(c echo.Context) error {
		resp := Response{Action: "listCluster", StatusCode: 0}
		clusters, err := listCluster()
		if err != nil {
			log.Error(err)
			resp.StatusCode = 400
			resp.Message = err.Error()
			return c.JSON(http.StatusBadRequest, resp)
		}
		resp.Payload = clusters
		log.Debugf("cluster list: %+v", clusters)
		return c.JSON(http.StatusOK, resp)
	})
	// Get cluster detail
	e.GET("/cluster", func(c echo.Context) error {
		clusterName := c.QueryParam("clusterName")
		resp := Response{Action: "getCluster", StatusCode: 0}
		cluster, err := getCluster(clusterName)
		if err != nil {
			resp.StatusCode = 400
			log.Error(err)
			if err == ClusterNotFound {
				resp.StatusCode = 404
			}
			resp.Message = err.Error()
			return c.JSON(http.StatusBadRequest, resp)
		}
		resp.Payload = []*Cluster{cluster}
		log.Debugf("cluster: %+v", cluster)
		return c.JSON(http.StatusOK, resp)
	})
	// Create cluster
	e.POST("/cluster", func(c echo.Context) error {
		cluster := &Cluster{}
		resp := Response{Action: "createCluster", StatusCode: 0}
		if err := c.Bind(cluster); err != nil {
			log.Error(err)
			resp.StatusCode = 400
			resp.Message = err.Error()
			return c.JSON(http.StatusBadRequest, resp)
		}
		log.Debugf("request cluster: %+v", cluster)
		if cluster.Name == "" {
			resp.StatusCode = 400
			resp.Message = "cluster name is empty"
		}
		err := createCluster(cluster)
		if err != nil {
			log.Error(err)
			resp.StatusCode = 400
			resp.Message = err.Error()
			return c.JSON(http.StatusBadRequest, resp)
		}
		resp.Message = fmt.Sprintf("tidb cluster %s created", cluster.Name)
		return c.JSON(http.StatusCreated, resp)
	})
	// Resize cluster
	e.PUT("/cluster", func(c echo.Context) error {
		resp := Response{Action: "resizeCluster", StatusCode: 0}
		clusterName := c.QueryParam("clusterName")
		if clusterName == "" {
			resp.StatusCode = 400
			resp.Message = "cluster name is empty"
			return c.JSON(http.StatusBadRequest, resp)
		}
		cluster := &Cluster{}
		if err := c.Bind(cluster); err != nil {
			log.Error(err)
			resp.StatusCode = 400
			resp.Message = err.Error()
			return c.JSON(http.StatusBadRequest, resp)
		}
		log.Debugf("request cluster: %+v", cluster)
		err := resizeCluster(clusterName, *cluster)
		if err != nil {
			log.Error(err)
			resp.StatusCode = 400
			resp.Message = err.Error()
			return c.JSON(http.StatusBadRequest, resp)
		}
		resp.Message = fmt.Sprintf("tidb cluster %s resized", clusterName)
		return c.JSON(http.StatusAccepted, resp)
	})
	// Delete cluster
	e.DELETE("/cluster", func(c echo.Context) error {
		resp := Response{Action: "deleteCluster", StatusCode: 0}
		clusterName := c.QueryParam("clusterName")
		if clusterName == "" {
			resp.StatusCode = 400
			resp.Message = "instance id empty"
			return c.JSON(http.StatusBadRequest, resp)
		}
		err := deleteCluster(clusterName)
		if err != nil {
			log.Error(err)
			resp.StatusCode = 400
			resp.Message = err.Error()
			return c.JSON(http.StatusBadRequest, resp)
		}
		resp.Message = fmt.Sprintf("tidb cluster %s deleted", clusterName)
		return c.JSON(http.StatusAccepted, resp)
	})
	e.Logger.Fatal(e.Start(":2333"))
}
