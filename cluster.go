package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ngaut/log"

	"github.com/pingcap/tidb-operator/pkg/controller"
	"github.com/pingcap/tidb-operator/pkg/spec"

	"k8s.io/client-go/pkg/api/v1"
	"k8s.io/client-go/pkg/labels"
)

const (
	urlPrefix = "/apis/pingcap.com/v1"
)

var (
	ClusterNotFound = errors.New("tidb cluster not found")
)

type PodStatus struct {
	Name   string `json:"name"`
	PodIP  string `json:"pod_ip"`
	NodeIP string `json:"node_ip"`
	Status string `json:"status"`
}

type Cluster struct {
	Name          string `json:"name"`
	PdCount       int    `json:"pd_count"`
	TikvCount     int    `json:"tikv_count"`
	TidbCount     int    `json:"tidb_count"`
	PdImage       string `json:"pd_image,omitempty"`
	TidbImage     string `json:"tidb_image,omitempty"`
	TikvImage     string `json:"tikv_image,omitempty"`
	ServiceType   string `json:"service_type,omitempty"`
	TidbLease     int    `json:"tidb_lease,omitempty"`
	EnableMonitor bool   `json:"enable_monitor,omitempty"`
	RootPassword  string `json:"root_password"`
	// response info
	CreatedAt   time.Time   `json:"created_at,omitempty"`
	TidbIP      string      `json:"tidb_ip,omitempty"`
	TidbPort    int         `json:"tidb_port,omitempty"`
	MonitorIP   string      `json:"monitor_ip,omitempty"`
	MonitorPort int         `json:"monitor_port,omitempty"`
	PdStatus    []PodStatus `json:"pd_status,omitempty"`
	TidbStatus  []PodStatus `json:"tidb_status,omitempty"`
	TikvStatus  []PodStatus `json:"tikv_status,omitempty"`
}

type clusterConfig struct {
	MetricsAddr string
}

func genConfig(clusterName string, enableMonitor bool, lease int) (map[string]string, error) {
	pdConfig := bytes.NewBuffer([]byte{})
	tikvConfig := bytes.NewBuffer([]byte{})
	metricsAddr := ""
	if enableMonitor {
		metricsAddr = fmt.Sprintf("%s-tidb-monitor-pushgateway:9091", clusterName)
	}
	c := &clusterConfig{
		MetricsAddr: metricsAddr,
	}
	err := pdConfigTmpl.Execute(pdConfig, c)
	if err != nil {
		return nil, err
	}
	err = tikvConfigTmpl.Execute(tikvConfig, c)
	if err != nil {
		return nil, err
	}
	config := map[string]string{
		"pd-config":         pdConfig.String(),
		"tikv-config":       tikvConfig.String(),
		"tidb.lease":        strconv.Itoa(lease),
		"tidb.metrics-addr": metricsAddr,
	}
	if enableMonitor {
		config["prometheus-config"] = `global:
  scrape_interval: 15s
  evaluation_interval: 15s
  labels:
    monitor: 'prometheus'
scrape_configs:
  - job_name: 'tidb-cluster'
    scrape_interval: 5s
    honor_labels: true
    static_configs:
      - targets: ['127.0.0.1:9091']
        labels:
          cluster: 'tidb-cluster'
`
	}
	return config, nil
}

func createCluster(c *Cluster) error {
	config, err := genConfig(c.Name, c.EnableMonitor, c.TidbLease)
	if err != nil {
		return err
	}
	if c.EnableMonitor {
		err := createTidbMonitor(c.Name)
		if err != nil {
			return err
		}
	}
	s := spec.TiDBCluster{
		ObjectMeta: v1.ObjectMeta{
			Name: c.Name,
		},
		Spec: spec.ClusterSpec{
			PD: &spec.ComponentSpec{
				Size:         c.PdCount,
				Image:        c.PdImage,
				NodeSelector: map[string]string{},
			},
			TiDB: &spec.ComponentSpec{
				Size:         c.TidbCount,
				Image:        c.TidbImage,
				NodeSelector: map[string]string{},
			},
			TiKV: &spec.ComponentSpec{
				Size:         c.TikvCount,
				Image:        c.TikvImage,
				NodeSelector: map[string]string{},
			},
			Paused:  false,
			Config:  config,
			Service: spec.Service{Type: c.ServiceType},
		},
	}
	j, err := json.Marshal(s)
	result := kubeCli.CoreV1().RESTClient().
		Post().
		AbsPath(urlPrefix).
		Namespace(namespace).
		Resource(tprName).
		Body(j).
		Do()
	b, err := result.Raw()
	if err != nil {
		return err
	}
	log.Debugf("response: %s", b)
	return nil
}

func listCluster() ([]*Cluster, error) {
	clusterList := &controller.TiDBClusterList{}
	req := kubeCli.CoreV1().RESTClient().
		Get().
		AbsPath(urlPrefix).
		Namespace(namespace).
		Resource(tprName)
	result := req.Do()
	b, err := result.Raw()
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(b, clusterList)
	if err != nil {
		return nil, err
	}
	clusters := []*Cluster{}
	for _, item := range clusterList.Items {
		c := &Cluster{
			Name:      item.ObjectMeta.Name,
			CreatedAt: item.ObjectMeta.CreationTimestamp.Time,
			PdCount:   item.Spec.PD.Size,
			TidbCount: item.Spec.TiDB.Size,
			TikvCount: item.Spec.TiKV.Size,
		}
		clusters = append(clusters, c)
	}
	return clusters, nil
}

func getCluster(clusterName string) (*Cluster, error) {
	var (
		c           = &Cluster{}
		clusterList = &controller.TiDBClusterList{}
	)
	req := kubeCli.CoreV1().RESTClient().
		Get().
		AbsPath(urlPrefix).
		Namespace(namespace).
		Resource(tprName)
	result := req.Do()
	b, err := result.Raw()
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(b, clusterList)
	if err != nil {
		return nil, err
	}
	if len(clusterList.Items) == 0 {
		return nil, ClusterNotFound
	}
	for _, item := range clusterList.Items {
		if item.ObjectMeta.Name == clusterName {
			c = &Cluster{
				Name:      item.ObjectMeta.Name,
				CreatedAt: item.ObjectMeta.CreationTimestamp.Time,
				PdCount:   item.Spec.PD.Size,
				TidbCount: item.Spec.TiDB.Size,
				TikvCount: item.Spec.TiKV.Size,
				PdImage:   item.Spec.PD.Image,
				TidbImage: item.Spec.TiDB.Image,
				TikvImage: item.Spec.TiKV.Image,
			}
			break
		}
	}
	option := v1.ListOptions{
		LabelSelector: labels.SelectorFromSet(map[string]string{
			"tidb_cluster": clusterName,
			"owner":        "tidb-cluster",
		}).String(),
	}
	pods, err := kubeCli.CoreV1().Pods(namespace).List(option)
	if err != nil {
		return nil, err
	}
	for _, pod := range pods.Items {
		if pod.ObjectMeta.Labels != nil {
			switch pod.ObjectMeta.Labels["app"] {
			case "pd":
				status := PodStatus{
					Name:   pod.ObjectMeta.GetName(),
					PodIP:  pod.Status.PodIP,
					NodeIP: pod.Status.HostIP,
					Status: string(pod.Status.Phase),
				}
				c.PdStatus = append(c.PdStatus, status)
			case "tikv":
				status := PodStatus{
					Name:   pod.ObjectMeta.GetName(),
					PodIP:  pod.Status.PodIP,
					NodeIP: pod.Status.HostIP,
					Status: string(pod.Status.Phase),
				}
				c.TikvStatus = append(c.TikvStatus, status)
			case "tidb":
				status := PodStatus{
					Name:   pod.ObjectMeta.GetName(),
					PodIP:  pod.Status.PodIP,
					NodeIP: pod.Status.HostIP,
					Status: string(pod.Status.Phase),
				}
				c.TidbStatus = append(c.TidbStatus, status)
			case "tidb-monitor", "configure-grafana":
				log.Infof("tidb monitor: %s", pod.ObjectMeta.Labels["app"])
			default:
				log.Warnf("unexpected pod type %s", pod.ObjectMeta.Labels["app"])
			}
		}
	}
	monitorSvc, err := kubeCli.CoreV1().Services(namespace).Get(fmt.Sprintf("%s-tidb-monitor-grafana", clusterName))
	if err != nil {
		log.Errorf("failed to get tidb monitor service(%s-tidb-monitor-grafana): %v", clusterName, err)
	} else {
		c.MonitorIP, c.MonitorPort, _ = getAddr(monitorSvc)
	}
	tidbSvc, err := kubeCli.CoreV1().Services(namespace).Get(fmt.Sprintf("%s-tidb", clusterName))
	if err != nil {
		log.Errorf("failed to get tidb service(%s-tidb): %v", clusterName, err)
	} else {
		c.TidbIP, c.TidbPort, _ = getAddr(tidbSvc)
	}
	return c, nil
}

func getAddr(svc *v1.Service) (string, int, error) {
	var (
		ip   string
		port int
	)
	switch svc.Spec.Type {
	case "NodePort":
		port = int(svc.Spec.Ports[0].NodePort)
		nodes, err := kubeCli.CoreV1().Nodes().List(v1.ListOptions{}) // TODO: cache it globally
		if err != nil {
			return "", 0, err
		}
		ips := []string{}
		for _, node := range nodes.Items {
			if node.Spec.Unschedulable {
				continue // skip unschedulable node
			}
			for _, a := range node.Status.Addresses {
				if a.Type == "Hostname" { // TODO: which address is more correct LegacyHostIP, InternalIP, Hostname
					ips = append(ips, a.Address)
				}
			}
		}
		ip = strings.Join(ips, ",")
	case "LoadBalancer":
		ip = svc.Status.LoadBalancer.Ingress[0].IP
	default: // ClusterIP
		ip = svc.Spec.ClusterIP
		port = int(svc.Spec.Ports[0].Port)
	}
	return ip, port, nil
}

func resizeCluster(clusterName string, c Cluster) error {
	s := spec.TiDBCluster{
		ObjectMeta: v1.ObjectMeta{
			Name: clusterName,
		},
		Spec: spec.ClusterSpec{
			PD: &spec.ComponentSpec{
				Size:         c.PdCount,
				Image:        c.PdImage,
				NodeSelector: map[string]string{},
			},
			TiDB: &spec.ComponentSpec{
				Size:         c.TidbCount,
				Image:        c.TidbImage,
				NodeSelector: map[string]string{},
			},
			TiKV: &spec.ComponentSpec{
				Size:         c.TikvCount,
				Image:        c.TikvImage,
				NodeSelector: map[string]string{},
			},
			Paused: false,
			// Config:  config,
			Service: spec.Service{Type: c.ServiceType},
		},
	}
	j, err := json.Marshal(s)
	if err != nil {
		log.Error(err)
		return err
	}
	result := kubeCli.CoreV1().RESTClient().
		Put().
		AbsPath(urlPrefix).
		Namespace(namespace).
		Resource(tprName).
		Name(clusterName).
		Body(j).
		Do()
	b, err := result.Raw()
	if err != nil {
		return err
	}
	log.Debugf("response: %s", b)
	return nil
}

func deleteCluster(clusterName string) error {
	result := kubeCli.CoreV1().RESTClient().
		Delete().
		AbsPath(urlPrefix).
		Namespace(namespace).
		Resource(tprName).
		Name(clusterName).
		Do()
	b, err := result.Raw()
	if err != nil {
		return err
	}
	log.Debugf("response: %s", b)
	err = deleteTidbMonitor(clusterName)
	if err != nil {
		return err
	}
	return nil
}
