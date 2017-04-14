package main

import (
	"fmt"

	"github.com/ngaut/log"
	"k8s.io/client-go/pkg/api/v1"
	batchv1 "k8s.io/client-go/pkg/apis/batch/v1"
	extensionsv1beta1 "k8s.io/client-go/pkg/apis/extensions/v1beta1"
	"k8s.io/client-go/pkg/labels"
	"k8s.io/client-go/pkg/util/intstr"
)

func createServices(clusterName string) error {
	labels := map[string]string{"app": "tidb-monitor", "tidb_cluster": clusterName, "owner": "tidb-cluster"}
	pushgatewaySvc := &v1.Service{
		ObjectMeta: v1.ObjectMeta{
			Name:   fmt.Sprintf("%s-tidb-monitor-pushgateway", clusterName),
			Labels: labels,
		},
		Spec: v1.ServiceSpec{
			Ports: []v1.ServicePort{
				{
					Name:       "pushgateway",
					Port:       9091,
					TargetPort: intstr.FromInt(9091),
					Protocol:   v1.ProtocolTCP,
				},
			},
			Selector: labels,
		},
	}
	_, err := kubeCli.CoreV1().Services(namespace).Create(pushgatewaySvc)
	if err != nil {
		log.Error(err)
		return err
	}
	grafanaSvc := &v1.Service{
		ObjectMeta: v1.ObjectMeta{
			Name:   fmt.Sprintf("%s-tidb-monitor-grafana", clusterName),
			Labels: labels,
		},
		Spec: v1.ServiceSpec{
			Type: "NodePort", // NodePort, ClusterIP, LoadBalancer
			Ports: []v1.ServicePort{
				{
					Name:       "grafana",
					Port:       3000,
					TargetPort: intstr.FromInt(3000),
					Protocol:   v1.ProtocolTCP,
				},
			},
			Selector: labels,
		},
	}
	_, err = kubeCli.CoreV1().Services(namespace).Create(grafanaSvc)
	if err != nil {
		log.Error(err)
		return err
	}
	return nil
}

func createMonitorPods(clusterName string) error {
	labels := map[string]string{
		"app":          "tidb-monitor",
		"tidb_cluster": clusterName,
		"owner":        "tidb-cluster",
	}
	deploy := &extensionsv1beta1.Deployment{
		ObjectMeta: v1.ObjectMeta{
			Name:   fmt.Sprintf("%s-tidb-monitor", clusterName),
			Labels: labels,
		},
		Spec: extensionsv1beta1.DeploymentSpec{
			Template: v1.PodTemplateSpec{
				ObjectMeta: v1.ObjectMeta{
					Name:   "tidb-monitor",
					Labels: labels,
				},
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							Name:  "pushgateway",
							Image: "localhost:5000/pushgateway:v0.3.1",
							Ports: []v1.ContainerPort{
								{
									Name:          "pushgateway",
									ContainerPort: int32(9091),
									Protocol:      v1.ProtocolTCP,
								},
							},
							VolumeMounts: []v1.VolumeMount{
								{Name: "timezone", ReadOnly: true, MountPath: "/etc/localtime"},
							},
						},
						{
							Name:  "prometheus",
							Image: "localhost:5000/prometheus:v1.5.2",
							Ports: []v1.ContainerPort{
								{
									Name:          "pushgateway",
									ContainerPort: int32(9090),
									Protocol:      v1.ProtocolTCP,
								},
							},
							VolumeMounts: []v1.VolumeMount{
								{Name: "timezone", ReadOnly: true, MountPath: "/etc/localtime"},
								{Name: "prometheus-config", ReadOnly: true, MountPath: "/etc/prometheus"},
								{Name: "prometheus-data", MountPath: "/prometheus"},
							},
						},
						{
							Name:  "grafana",
							Image: "localhost:5000/grafana:4.2.0",
							Ports: []v1.ContainerPort{
								{
									Name:          "grafana",
									ContainerPort: int32(3000),
									Protocol:      v1.ProtocolTCP,
								},
							},
							VolumeMounts: []v1.VolumeMount{
								{Name: "timezone", ReadOnly: true, MountPath: "/etc/localtime"},
								{Name: "grafana-data", MountPath: "/data"},
							},
							Env: []v1.EnvVar{
								{Name: "GF_PATHS_DATA", Value: "/data"},
							},
						},
					},
					Volumes: []v1.Volume{
						{Name: "timezone", VolumeSource: v1.VolumeSource{HostPath: &v1.HostPathVolumeSource{Path: "/etc/localtime"}}},
						{Name: "prometheus-data", VolumeSource: v1.VolumeSource{EmptyDir: &v1.EmptyDirVolumeSource{}}},
						{Name: "grafana-data", VolumeSource: v1.VolumeSource{EmptyDir: &v1.EmptyDirVolumeSource{}}},
						{Name: "prometheus-config", VolumeSource: v1.VolumeSource{
							ConfigMap: &v1.ConfigMapVolumeSource{
								LocalObjectReference: v1.LocalObjectReference{Name: fmt.Sprintf("%s-config", clusterName)},
								Items:                []v1.KeyToPath{{Key: "prometheus-config", Path: "prometheus.yml"}},
							},
						}},
					},
				},
			},
		},
	}
	_, err := kubeCli.ExtensionsV1beta1().Deployments(namespace).Create(deploy)
	if err != nil {
		log.Errorf("error creating tidb-monitor deployment: %+v", err)
		return err
	}
	return nil
}

func configGrafana(clusterName string) error {
	job := &batchv1.Job{
		ObjectMeta: v1.ObjectMeta{
			Name: fmt.Sprintf("%s-tidb-monitor-configure-grafana", clusterName),
			Labels: map[string]string{
				"app":          "configure-grafana",
				"tidb_cluster": clusterName,
			},
		},
		Spec: batchv1.JobSpec{
			Template: v1.PodTemplateSpec{
				ObjectMeta: v1.ObjectMeta{
					Name: "configure-grafana",
				},
				Spec: v1.PodSpec{
					RestartPolicy: v1.RestartPolicyOnFailure,
					Containers: []v1.Container{
						{
							Name:  "configure-grafana",
							Image: "localhost:5000/tidb-dashboard-installer",
							Args:  []string{fmt.Sprintf("%s-tidb-monitor-grafana:3000", clusterName)},
						},
					},
				},
			},
		},
	}
	_, err := kubeCli.BatchV1().Jobs(namespace).Create(job)
	if err != nil {
		log.Errorf("error creating configure grafana job: %+v", err)
		return err
	}
	return nil
}

func setPassword(clusterName string, password string) error {
	job := &batchv1.Job{
		ObjectMeta: v1.ObjectMeta{
			Name: fmt.Sprintf("%s-tidb-set-password", clusterName),
			Labels: map[string]string{
				"app":          "set-tidb-password",
				"tidb_cluster": clusterName,
				"owner":        "tidb-cluster",
			},
		},
		Spec: batchv1.JobSpec{
			Template: v1.PodTemplateSpec{
				ObjectMeta: v1.ObjectMeta{
					Name: "set-tidb-password",
				},
				Spec: v1.PodSpec{
					RestartPolicy: v1.RestartPolicyOnFailure,
					Containers: []v1.Container{
						{
							Name:    "set-tidb-password",
							Image:   "pingcap/tidb", // TODO: add mysql-client to tidb image, and support set password
							Command: []string{fmt.Sprintf("/usr/bin/mysql -h %s-tidb -P 4000 -u root -e \"set password for 'root'@'%' = $(TIDB_PASSWORD)\";", clusterName)},
							Env: []v1.EnvVar{
								{
									Name: "TIDB_PASSWORD",
									ValueFrom: &v1.EnvVarSource{
										SecretKeyRef: &v1.SecretKeySelector{
											LocalObjectReference: v1.LocalObjectReference{Name: fmt.Sprintf("%s-secret", clusterName)},
											Key:                  "tidb-password",
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
	_, err := kubeCli.BatchV1().Jobs(namespace).Create(job)
	if err != nil {
		log.Errorf("error creating tidb password setter job: %+v", err)
		return err
	}
	return nil
}

func createTidbMonitor(clusterName string) error {
	var err error
	if err != nil {
		return err
	}
	err = createServices(clusterName)
	if err != nil {
		return err
	}
	err = createMonitorPods(clusterName)
	if err != nil {
		return err
	}
	err = configGrafana(clusterName)
	if err != nil {
		return err
	}
	return nil
}

func deleteTidbMonitor(clusterName string) error {
	var err error
	err = kubeCli.CoreV1().Services(namespace).Delete(fmt.Sprintf("%s-tidb-monitor-grafana", clusterName), nil)
	if err != nil {
		log.Errorf("error deleting service %s-tidb-monitor-grafana: %+v", clusterName, err)
		return err
	}
	err = kubeCli.CoreV1().Services(namespace).Delete(fmt.Sprintf("%s-tidb-monitor-pushgateway", clusterName), nil)
	if err != nil {
		log.Errorf("error deleting service %s-tidb-monitor-pushgateway: %+v", clusterName, err)
		return err
	}
	err = kubeCli.CoreV1().ConfigMaps(namespace).Delete(fmt.Sprintf("%s-config", clusterName), nil)
	if err != nil {
		log.Errorf("error deleting configmap %s-tidb-monitor-configs: %+v", clusterName, err)
		return err
	}
	err = kubeCli.ExtensionsV1beta1().Deployments(namespace).Delete(fmt.Sprintf("%s-tidb-monitor", clusterName), nil)
	if err != nil {
		log.Errorf("error deleting deployment %s-tidb-monitor: %+v", clusterName, err)
		return err
	}
	option := v1.ListOptions{
		LabelSelector: labels.SelectorFromSet(map[string]string{
			"tidb_cluster": clusterName,
			"app":          "tidb-monitor",
			"owner":        "tidb-cluster",
		}).String(),
	}
	rss, err := kubeCli.ExtensionsV1beta1().ReplicaSets(namespace).List(option)
	if err != nil {
		return err
	}
	log.Debugf("tidb-monitor replicaset count: %d", len(rss.Items))
	for _, rs := range rss.Items {
		log.Debugf("deleting replicaset %s", rs.ObjectMeta.Name)
		err = kubeCli.ExtensionsV1beta1().ReplicaSets(namespace).Delete(rs.ObjectMeta.Name, nil)
		if err != nil {
			log.Errorf("error deleting replicaset %s: %v", rs.ObjectMeta.Name, err)
		}
	}
	err = kubeCli.BatchV1().Jobs(namespace).Delete(fmt.Sprintf("%s-tidb-monitor-configure-grafana", clusterName), nil)
	if err != nil {
		log.Errorf("error deleting configure-grafana job %s-tidb-monitor-configure-grafana: %+v", clusterName, err)
		return err
	}
	return nil
}
