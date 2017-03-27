package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"io/ioutil"
	"net/http"
	"time"

	"github.com/labstack/echo"
	"github.com/ngaut/log"
	"github.com/pingcap/tidb-operator/pkg/controller"
	k8sapi "k8s.io/kubernetes/pkg/api"
	clientset "k8s.io/kubernetes/pkg/client/clientset_generated/internalclientset"
	"k8s.io/kubernetes/pkg/client/restclient"
	"k8s.io/kubernetes/pkg/labels"
)

const (
	instanceType = "Normal"
)

var (
	kubeCli        *clientset.Clientset
	logLevel       string
	repo           string
	tikvConfigTmpl *template.Template
	pdConfigTmpl   *template.Template
)

type Instance struct {
	ID                 string    `json:"id"`
	Region             string    `json:"region,omitempty"`        // currently not used
	Zone               string    `json:"zone,omitempty"`          // currently not used
	AdminUsername      string    `json:"adminUsername,omitempty"` // currently not used
	AdminPassword      string    `json:"adminPassword,omitempty"` // currently not used
	Port               int       `json:"port,omitempty"`          // ulb frontend port, currently not used
	Binlog             bool      `json:"binlog,omitempty"`        // currently not used
	PdNodes            int       `json:"pdNodes"`
	TidbNodes          int       `json:"tidbNodes"`
	TikvNodes          int       `json:"tikvNodes"`
	PdNodeSpec         string    `json:"pdNodeSpec,omitempty"` // Normal, SSD, BigData, currently only support Normal
	TidbNodeSpec       string    `json:"tidbNodeSpec,omitempty"`
	TikvNodeSpec       string    `json:"tikvNodeSpec,omitempty"`
	BackupStartHour    int       `json:"backupStartHour,omitempty"`    // currently not used
	BackupIntervalDays int       `json:"backupIntervalDays,omitempty"` // currently not used
	BackupReserveDays  int       `json:"backupReserveDays,omitempty"`  // currently not used
	CreatedAt          time.Time `json:"createdAt,omitempty"`
	RunningStatus      string    `json:"runningStatus,omitempty"` // creating, running, terminated
	EIP                string    `json:"eip,omitempty"`
	MonitorIP          string    `json:"monitorIp,omitempty"` // grafana port 3000
}

type Response struct {
	Action     string      `json:"action"`
	StatusCode int         `json:"status_code"`
	Message    string      `json:"message,omitempty"`
	Payload    []*Instance `json:"payload,omitempty"`
}

type spec struct {
	Size         int               `json:"size"`
	Image        string            `json:"image"`
	NodeSelector map[string]string `json:"nodeSelector"`
}

type clusterSpec struct {
	APIVersion string            `json:"apiVersion"`
	Kind       string            `json:"kind"`
	Metadata   map[string]string `json:"metadata"`
	Spec       map[string]spec   `json:"spec"`
}

func init() {
	flag.StringVar(&logLevel, "L", "info", "log level: debug, info, warn, error, fatal (default \"info\")")
	flag.StringVar(&repo, "repo", "localhost:5000", "docker repository which tidb images are pulled")
	flag.Parse()
	log.SetLevelByString(logLevel)
	log.SetHighlighting(true)
	b1, err := ioutil.ReadFile("/pd.toml.tmpl")
	if err != nil {
		log.Fatal(err)
	}
	pdConfigTmpl = template.Must(template.New("pd-config").Parse(string(b1)))
	b2, err := ioutil.ReadFile("/tikv.toml.tmpl")
	if err != nil {
		log.Fatal(err)
	}
	tikvConfigTmpl = template.Must(template.New("tikv-config").Parse(string(b2)))
}

func createCluster(host, namespace string, instance Instance) error {
	err := createTidbMonitor(namespace, instance.ID)
	if err != nil {
		log.Errorf("error creating %s-tidb-monitor", instance.ID)
	}
	restClient, ok := kubeCli.Core().RESTClient().(*restclient.RESTClient)
	if !ok {
		log.Fatal("kubeclient initialize failed")
	}
	s := clusterSpec{
		APIVersion: "pingcap.com/v1",
		Kind:       "TidbCluster",
		Metadata:   map[string]string{"name": instance.ID},
		Spec: map[string]spec{
			"pd": spec{
				Size:         instance.PdNodes,
				Image:        fmt.Sprintf("%s/pd", repo),
				NodeSelector: map[string]string{"beta.kubernetes.io/instance-type": instanceType},
			},
			"tikv": spec{
				Size:         instance.TikvNodes,
				Image:        fmt.Sprintf("%s/tikv", repo),
				NodeSelector: map[string]string{"beta.kubernetes.io/instance-type": instanceType},
			},
			"tidb": spec{
				Size:         instance.TidbNodes,
				Image:        fmt.Sprintf("%s/tidb", repo),
				NodeSelector: map[string]string{"beta.kubernetes.io/instance-type": instanceType},
			},
		},
	}
	j, err := json.Marshal(s)
	log.Debugf("cluster spec: %s", string(j))
	u := fmt.Sprintf("%s/apis/pingcap.com/v1/namespaces/%s/tidbclusters", host, namespace)
	log.Debugf("request url: %s", u)
	req, err := http.NewRequest("POST", u, bytes.NewBuffer(j))
	if err != nil {
		return err
	}
	resp, err := restClient.Client.Do(req)
	b, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	log.Debugf("response: %s", string(b))
	return nil
}

func resizeCluster(host, namespace string, id string, instance Instance) error {
	restClient, ok := kubeCli.Core().RESTClient().(*restclient.RESTClient)
	if !ok {
		log.Fatal("kubeclient initialize failed")
	}
	s := clusterSpec{
		APIVersion: "pingcap.com/v1",
		Kind:       "TidbCluster",
		Metadata:   map[string]string{"name": instance.ID},
		Spec: map[string]spec{
			"pd": spec{
				Size:         instance.PdNodes,
				Image:        fmt.Sprintf("%s/pd", repo),
				NodeSelector: map[string]string{"beta.kubernetes.io/instance-type": instanceType},
			},
			"tikv": spec{
				Size:         instance.TikvNodes,
				Image:        fmt.Sprintf("%s/tikv", repo),
				NodeSelector: map[string]string{"beta.kubernetes.io/instance-type": instanceType},
			},
			"tidb": spec{
				Size:         instance.TidbNodes,
				Image:        fmt.Sprintf("%s/tidb", repo),
				NodeSelector: map[string]string{"beta.kubernetes.io/instance-type": instanceType},
			},
		},
	}
	j, err := json.Marshal(s)
	log.Debugf("cluster spec: %s", string(j))
	u := fmt.Sprintf("%s/apis/pingcap.com/v1/namespaces/%s/tidbclusters/%s", host, namespace, id)
	log.Debugf("request url: %s", u)
	req, err := http.NewRequest("PUT", u, bytes.NewBuffer(j))
	if err != nil {
		return err
	}
	resp, err := restClient.Client.Do(req)
	b, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	log.Debugf("response: %s", string(b))
	return nil
}

func deleteCluster(host, namespace, id string) error {
	restClient, ok := kubeCli.Core().RESTClient().(*restclient.RESTClient)
	if !ok {
		log.Fatal("kubeclient initialize failed")
	}
	u := fmt.Sprintf("%s/apis/pingcap.com/v1/namespaces/%s/tidbclusters/%s", host, namespace, id)
	log.Debugf("request url: %s", u)
	req, err := http.NewRequest("DELETE", u, nil)
	if err != nil {
		return err
	}
	resp, err := restClient.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	log.Debugf("response: %s", string(b))
	err = deleteTidbMonitor(namespace, id)
	if err != nil {
		log.Errorf("error deleting %s-tidb-monitor", id)
	}
	return nil
}

func listCluster(host, namespace string) ([]*Instance, error) {
	clusterList := &controller.TiDBClusterList{}
	restClient, ok := kubeCli.Core().RESTClient().(*restclient.RESTClient)
	if !ok {
		log.Fatal("kubeclient initialize failed")
	}
	resp, err := restClient.Client.Get(fmt.Sprintf("%s/apis/pingcap.com/v1/namespaces/%s/tidbclusters", host, namespace))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	dec := json.NewDecoder(resp.Body)
	err = dec.Decode(clusterList)
	if err != nil {
		return nil, err
	}
	instances := []*Instance{}
	for _, item := range clusterList.Items {
		size := make(map[string]int)
		size["pd"] = item.Spec.PD.Size
		size["tidb"] = item.Spec.TiDB.Size
		size["tikv"] = item.Spec.TiKV.Size
		instance := &Instance{
			ID:        item.ObjectMeta.Name,
			CreatedAt: item.ObjectMeta.CreationTimestamp.Time,
			PdNodes:   item.Spec.PD.Size,
			TidbNodes: item.Spec.TiDB.Size,
			TikvNodes: item.Spec.TiKV.Size,
		}
		instances = append(instances, instance)
	}
	return instances, nil
}

func getCluster(host, namespace, clusterName string) (*Instance, error) {
	instance := &Instance{ID: clusterName, RunningStatus: "creating"}
	option := k8sapi.ListOptions{
		LabelSelector: labels.SelectorFromSet(map[string]string{
			"tidb_cluster": clusterName,
		}),
	}
	pods, err := kubeCli.Pods(namespace).List(option)
	if err != nil {
		return nil, err
	}
	if len(pods.Items) == 0 { // no running pods, terminated
		instance.RunningStatus = "terminated"
		return instance, nil
	}
	for _, pod := range pods.Items {
		if pod.ObjectMeta.Labels != nil {
			switch pod.ObjectMeta.Labels["app"] {
			case "pd":
				if pod.Status.Phase == "Running" {
					instance.PdNodes++
					continue
				}
			case "tikv":
				if pod.Status.Phase == "Running" {
					instance.TikvNodes++
					continue
				}
			case "tidb":
				if pod.Status.Phase == "Running" {
					instance.TidbNodes++
					continue
				}
			default:
				log.Warnf("unexpected pod type %s", pod.ObjectMeta.Labels["app"])
			}
		}
	}
	if instance.PdNodes == 0 || instance.TidbNodes == 0 || instance.TikvNodes == 0 {
		return instance, nil // creating
	}
	monitorSvc, err := kubeCli.Services(namespace).Get(fmt.Sprintf("%s-tidb-monitor-grafana", clusterName))
	if err == nil && len(monitorSvc.Status.LoadBalancer.Ingress) != 0 {
		instance.MonitorIP = monitorSvc.Status.LoadBalancer.Ingress[0].IP
	}
	svc, err := kubeCli.Services(namespace).Get(fmt.Sprintf("%s-tidb", clusterName))
	if err == nil && len(svc.Status.LoadBalancer.Ingress) != 0 {
		instance.RunningStatus = "running"
		instance.EIP = svc.Status.LoadBalancer.Ingress[0].IP
		return instance, nil
	}
	return instance, nil // pod is running, EIP not found, cluster is creating
}

func main() {
	cfg, err := restclient.InClusterConfig()
	if err != nil {
		panic(err)
	}
	cfg.QPS = 100
	cfg.Burst = 100
	kubeCli = clientset.NewForConfigOrDie(cfg)
	host := cfg.Host
	namespace := "default"

	e := echo.New()
	// List cluster
	e.GET("/instances", func(c echo.Context) error {
		resp := Response{Action: "listCluster", StatusCode: 0}
		instances, err := listCluster(host, namespace)
		if err != nil {
			log.Error(err)
			resp.StatusCode = 400
			resp.Message = err.Error()
			return c.JSON(http.StatusBadRequest, resp)
		}
		resp.Payload = instances
		log.Debugf("cluster list: %+v", instances)
		return c.JSON(http.StatusOK, resp)
	})
	// Get cluster
	e.GET("/instance", func(c echo.Context) error {
		id := c.QueryParam("instId") // using instId as clusterName
		resp := Response{Action: "getCluster", StatusCode: 0}
		instance, err := getCluster(host, namespace, id)
		if err != nil {
			log.Error(err)
			resp.StatusCode = 400
			resp.Message = err.Error()
			return c.JSON(http.StatusBadRequest, resp)
		}
		resp.Payload = []*Instance{instance}
		log.Debugf("cluster: %+v", instance)
		return c.JSON(http.StatusOK, resp)
	})
	// Create cluster
	e.POST("/instance", func(c echo.Context) error {
		instance := &Instance{}
		resp := Response{Action: "createCluster", StatusCode: 0}
		if err := c.Bind(instance); err != nil {
			log.Error(err)
			resp.StatusCode = 400
			resp.Message = err.Error()
			return c.JSON(http.StatusBadRequest, resp)
		}
		log.Debugf("instance: %+v", instance)
		if instance.ID == "" {
			resp.StatusCode = 400
			resp.Message = "instance id empty"
			return c.JSON(http.StatusBadRequest, resp)
		}
		err := createCluster(host, namespace, *instance)
		if err != nil {
			log.Error(err)
			resp.StatusCode = 400
			resp.Message = err.Error()
			return c.JSON(http.StatusBadRequest, resp)
		}
		resp.Message = fmt.Sprintf("tidb instance %s created", instance.ID)
		return c.JSON(http.StatusCreated, resp)
	})
	// Resize cluster
	e.PUT("/instance", func(c echo.Context) error {
		resp := Response{Action: "resizeCluster", StatusCode: 0}
		id := c.QueryParam("instId")
		if id == "" {
			resp.StatusCode = 400
			resp.Message = "instance id empty"
			return c.JSON(http.StatusBadRequest, resp)
		}
		instance := &Instance{}
		if err := c.Bind(instance); err != nil {
			log.Error(err)
			resp.StatusCode = 400
			resp.Message = err.Error()
			return c.JSON(http.StatusBadRequest, resp)
		}
		log.Debugf("instance: %+v", instance)
		err := resizeCluster(host, namespace, id, *instance)
		if err != nil {
			log.Error(err)
			resp.StatusCode = 400
			resp.Message = err.Error()
			return c.JSON(http.StatusBadRequest, resp)
		}
		resp.Message = fmt.Sprintf("tidb instance %s resized", id)
		return c.JSON(http.StatusAccepted, resp)
	})
	// Delete cluster
	e.DELETE("/instance", func(c echo.Context) error {
		resp := Response{Action: "deleteCluster", StatusCode: 0}
		id := c.QueryParam("instId")
		if id == "" {
			resp.StatusCode = 400
			resp.Message = "instance id empty"
			return c.JSON(http.StatusBadRequest, resp)
		}
		err := deleteCluster(host, namespace, id)
		if err != nil {
			log.Error(err)
			resp.StatusCode = 400
			resp.Message = err.Error()
			return c.JSON(http.StatusBadRequest, resp)
		}
		resp.Message = fmt.Sprintf("tidb instance %s deleted", id)
		return c.JSON(http.StatusAccepted, resp)
	})
	e.Logger.Fatal(e.Start(":2333"))
}
