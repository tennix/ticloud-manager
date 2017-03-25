package main

import (
	"bytes"
	"fmt"

	"github.com/ngaut/log"

	"k8s.io/kubernetes/pkg/api"
	batchapi "k8s.io/kubernetes/pkg/apis/batch"
	extensionsapi "k8s.io/kubernetes/pkg/apis/extensions"
	"k8s.io/kubernetes/pkg/util/intstr"
)

type clusterConfig struct {
	ClusterName string
}

func createConfigs(namespace, clusterName string) error {
	pdConfig := bytes.NewBuffer([]byte{})
	tikvConfig := bytes.NewBuffer([]byte{})
	c := &clusterConfig{ClusterName: fmt.Sprintf("%s-tidb-monitor-pushgateway", clusterName)}
	err := pdConfigTmpl.Execute(pdConfig, c)
	if err != nil {
		log.Fatal(err)
	}
	err = tikvConfigTmpl.Execute(tikvConfig, c)
	if err != nil {
		log.Fatal(err)
	}
	cm := &api.ConfigMap{
		ObjectMeta: api.ObjectMeta{
			Name:   fmt.Sprintf("%s-configs", clusterName),
			Labels: map[string]string{"app": "tidb-monitor", "tidb_cluster": clusterName},
		},
		Data: map[string]string{
			"prometheus-config": `global:
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
`,
			"pd-config": pdConfig.String(),

			"tikv-config": tikvConfig.String(),
		},
	}
	_, err = kubeCli.ConfigMaps(namespace).Create(cm)
	if err != nil {
		return err
	}
	return nil
}

func exposeGrafana(namespace, clusterName string) error {
	labels := map[string]string{"app": "tidb-monitor", "tidb_cluster": clusterName}
	pushgatewaySvc := &api.Service{
		ObjectMeta: api.ObjectMeta{
			Name:   fmt.Sprintf("%s-tidb-monitor-pushgateway", clusterName),
			Labels: labels,
		},
		Spec: api.ServiceSpec{
			Ports: []api.ServicePort{
				{
					Name:       "pushgateway",
					Port:       9091,
					TargetPort: intstr.FromInt(9091),
					Protocol:   api.ProtocolTCP,
				},
			},
			Selector: labels,
		},
	}
	_, err := kubeCli.Services(namespace).Create(pushgatewaySvc)
	if err != nil {
		log.Error(err)
		return err
	}
	grafanaSvc := &api.Service{
		ObjectMeta: api.ObjectMeta{
			Name:   fmt.Sprintf("%s-tidb-monitor-grafana", clusterName),
			Labels: labels,
		},
		Spec: api.ServiceSpec{
			Type: "NodePort", // NodePort, ClusterIP, LoadBalancer
			Ports: []api.ServicePort{
				{
					Name:       "grafana",
					Port:       3000,
					TargetPort: intstr.FromInt(3000),
					Protocol:   api.ProtocolTCP,
				},
			},
			Selector: labels,
		},
	}
	_, err = kubeCli.Services(namespace).Create(grafanaSvc)
	if err != nil {
		log.Error(err)
		return err
	}
	return nil
}

func createMonitorPods(namespace, clusterName string) error {
	labels := map[string]string{
		"app":          "tidb-monitor",
		"tidb_cluster": clusterName,
	}
	deploy := &extensionsapi.Deployment{
		ObjectMeta: api.ObjectMeta{
			Name:   fmt.Sprintf("%s-tidb-monitor", clusterName),
			Labels: labels,
		},
		Spec: extensionsapi.DeploymentSpec{
			Replicas: 1,
			Template: api.PodTemplateSpec{
				ObjectMeta: api.ObjectMeta{
					Name:   "tidb-monitor",
					Labels: labels,
				},
				Spec: api.PodSpec{
					Containers: []api.Container{
						{
							Name:  "pushgateway",
							Image: fmt.Sprintf("%s/pushgateway:v0.3.1", repo),
							Ports: []api.ContainerPort{
								{
									Name:          "pushgateway",
									ContainerPort: int32(9091),
									Protocol:      api.ProtocolTCP,
								},
							},
							VolumeMounts: []api.VolumeMount{
								{Name: "timezone", ReadOnly: true, MountPath: "/etc/localtime"},
							},
						},
						{
							Name:  "prometheus",
							Image: fmt.Sprintf("%s/prometheus:v1.5.2", repo),
							Ports: []api.ContainerPort{
								{
									Name:          "pushgateway",
									ContainerPort: int32(9090),
									Protocol:      api.ProtocolTCP,
								},
							},
							VolumeMounts: []api.VolumeMount{
								{Name: "timezone", ReadOnly: true, MountPath: "/etc/localtime"},
								{Name: "prometheus-config", ReadOnly: true, MountPath: "/etc/prometheus"},
								{Name: "prometheus-data", MountPath: "/prometheus"},
							},
						},
						{
							Name:  "grafana",
							Image: fmt.Sprintf("%s/grafana:4.2.0", repo),
							Ports: []api.ContainerPort{
								{
									Name:          "grafana",
									ContainerPort: int32(3000),
									Protocol:      api.ProtocolTCP,
								},
							},
							VolumeMounts: []api.VolumeMount{
								{Name: "timezone", ReadOnly: true, MountPath: "/etc/localtime"},
								{Name: "grafana-data", MountPath: "/data"},
							},
							Env: []api.EnvVar{
								{Name: "GF_PATHS_DATA", Value: "/data"},
							},
						},
					},
					Volumes: []api.Volume{
						{Name: "timezone", VolumeSource: api.VolumeSource{HostPath: &api.HostPathVolumeSource{Path: "/etc/localtime"}}},
						{Name: "prometheus-data", VolumeSource: api.VolumeSource{EmptyDir: &api.EmptyDirVolumeSource{}}},
						{Name: "grafana-data", VolumeSource: api.VolumeSource{EmptyDir: &api.EmptyDirVolumeSource{}}},
						{Name: "prometheus-config", VolumeSource: api.VolumeSource{
							ConfigMap: &api.ConfigMapVolumeSource{
								LocalObjectReference: api.LocalObjectReference{Name: fmt.Sprintf("%s-configs", clusterName)},
								Items:                []api.KeyToPath{{Key: "prometheus-config", Path: "prometheus.yml"}},
							},
						}},
					},
				},
			},
		},
	}
	_, err := kubeCli.Extensions().Deployments(namespace).Create(deploy)
	if err != nil {
		log.Errorf("error creating tidb-monitor deployment: %+v", err)
		return err
	}
	return nil
}

func configGrafana(namespace, clusterName string) error {
	job := &batchapi.Job{
		ObjectMeta: api.ObjectMeta{
			Name: fmt.Sprintf("%s-tidb-monitor-configure-grafana", clusterName),
			Labels: map[string]string{
				"app":          "configure-grafana",
				"tidb_cluster": clusterName,
			},
		},
		Spec: batchapi.JobSpec{
			Template: api.PodTemplateSpec{
				ObjectMeta: api.ObjectMeta{
					Name: fmt.Sprintf("configure-grafana"),
				},
				Spec: api.PodSpec{
					RestartPolicy: api.RestartPolicyOnFailure,
					Containers: []api.Container{
						{
							Name:  "configure-grafana",
							Image: fmt.Sprintf("%s/tidb-dashboard-installer", repo),
							Args:  []string{fmt.Sprintf("%s-tidb-monitor-grafana:3000", clusterName)},
						},
					},
				},
			},
		},
	}
	_, err := kubeCli.Batch().Jobs(namespace).Create(job)
	if err != nil {
		log.Errorf("error creating configure grafana job: %+v", err)
		return err
	}
	return nil
}

func createTidbMonitor(namespace, clusterName string) error {
	var err error
	err = createConfigs(namespace, clusterName)
	if err != nil {
		return err
	}
	err = exposeGrafana(namespace, clusterName)
	if err != nil {
		return err
	}
	err = createMonitorPods(namespace, clusterName)
	if err != nil {
		return err
	}
	err = configGrafana(namespace, clusterName)
	if err != nil {
		return err
	}
	return nil
}

func deleteTidbMonitor(namespace, clusterName string) error {
	var err error
	err = kubeCli.Services(namespace).Delete(fmt.Sprintf("%s-tidb-monitor-grafana", clusterName), nil)
	if err != nil {
		log.Errorf("error deleting service %s-tidb-monitor-grafana: %+v", clusterName, err)
		return err
	}
	err = kubeCli.Services(namespace).Delete(fmt.Sprintf("%s-tidb-monitor-pushgateway", clusterName), nil)
	if err != nil {
		log.Errorf("error deleting service %s-tidb-monitor-pushgateway: %+v", clusterName, err)
		return err
	}
	err = kubeCli.ConfigMaps(namespace).Delete(fmt.Sprintf("%s-configs", clusterName), nil)
	if err != nil {
		log.Errorf("error deleting configmap %s-tidb-monitor-configs: %+v", clusterName, err)
		return err
	}
	err = kubeCli.Extensions().Deployments(namespace).Delete(fmt.Sprintf("%s-tidb-monitor", clusterName), nil)
	if err != nil {
		log.Errorf("error deleting deployment %s-tidb-monitor: %+v", clusterName, err)
		return err
	}
	err = kubeCli.Batch().Jobs(namespace).Delete(fmt.Sprintf("%s-tidb-monitor-configure-grafana", clusterName), nil)
	if err != nil {
		log.Errorf("error deleting configure-grafana job %s-tidb-monitor-configure-grafana: %+v", clusterName, err)
		return err
	}
	return nil
}
