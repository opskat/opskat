package k8s

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

type NodeInfo struct {
	Name    string   `json:"name"`
	Status  string   `json:"status"`
	Roles   []string `json:"roles"`
	Version string   `json:"version"`
	CPU     string   `json:"cpu"`
	Memory  string   `json:"memory"`
	OS      string   `json:"os"`
	Arch    string   `json:"arch"`
}

type NamespaceInfo struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

type ClusterInfo struct {
	Version    string          `json:"version"`
	Platform   string          `json:"platform"`
	Nodes      []NodeInfo      `json:"nodes"`
	Namespaces []NamespaceInfo `json:"namespaces"`
}

func GetClusterInfo(ctx context.Context, kubeconfig, apiServer, token string) (*ClusterInfo, error) {
	var config *rest.Config
	var err error

	if kubeconfig != "" {
		clientCfg, err := clientcmd.Load([]byte(kubeconfig))
		if err != nil {
			return nil, fmt.Errorf("parse kubeconfig: %w", err)
		}
		config, err = clientcmd.NewDefaultClientConfig(*clientCfg, &clientcmd.ConfigOverrides{}).ClientConfig()
		if err != nil {
			return nil, fmt.Errorf("build rest config from kubeconfig: %w", err)
		}
	} else if apiServer != "" {
		config = &rest.Config{
			Host:        apiServer,
			BearerToken: token,
			TLSClientConfig: rest.TLSClientConfig{
				Insecure: true,
			},
			Timeout: 30 * time.Second,
		}
	} else {
		return nil, fmt.Errorf("kubeconfig or api_server is required")
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("create k8s clientset: %w", err)
	}

	info := &ClusterInfo{}

	serverVersion, err := clientset.Discovery().ServerVersion()
	if err != nil {
		return nil, fmt.Errorf("get server version: %w", err)
	}
	info.Version = serverVersion.GitVersion
	info.Platform = serverVersion.Platform

	nodeList, err := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	for _, node := range nodeList.Items {
		ni := NodeInfo{
			Name:    node.Name,
			Version: node.Status.NodeInfo.KubeletVersion,
			OS:      node.Status.NodeInfo.OperatingSystem,
			Arch:    node.Status.NodeInfo.Architecture,
			CPU:     node.Status.Capacity.Cpu().String(),
			Memory:  node.Status.Capacity.Memory().String(),
		}
		for _, cond := range node.Status.Conditions {
			if cond.Type == "Ready" {
				ni.Status = string(cond.Status)
			}
		}
		ni.Roles = getNodeRoles(&node)
		info.Nodes = append(info.Nodes, ni)
	}

	nsList, err := clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list namespaces: %w", err)
	}
	for _, ns := range nsList.Items {
		info.Namespaces = append(info.Namespaces, NamespaceInfo{
			Name:   ns.Name,
			Status: string(ns.Status.Phase),
		})
	}

	return info, nil
}

type NamespaceResources struct {
	Namespace       string `json:"namespace"`
	Pods            int    `json:"pods"`
	Deployments     int    `json:"deployments"`
	Services        int    `json:"services"`
	ConfigMaps      int    `json:"config_maps"`
	Secrets         int    `json:"secrets"`
	PVCs            int    `json:"pvcs"`
	ServiceAccounts int    `json:"service_accounts"`
}

func GetNamespaceResources(ctx context.Context, kubeconfig, apiServer, token, namespace string) (*NamespaceResources, error) {
	var config *rest.Config
	var err error

	if kubeconfig != "" {
		clientCfg, err := clientcmd.Load([]byte(kubeconfig))
		if err != nil {
			return nil, fmt.Errorf("parse kubeconfig: %w", err)
		}
		config, err = clientcmd.NewDefaultClientConfig(*clientCfg, &clientcmd.ConfigOverrides{}).ClientConfig()
		if err != nil {
			return nil, fmt.Errorf("build rest config from kubeconfig: %w", err)
		}
	} else if apiServer != "" {
		config = &rest.Config{
			Host:        apiServer,
			BearerToken: token,
			TLSClientConfig: rest.TLSClientConfig{
				Insecure: true,
			},
			Timeout: 30 * time.Second,
		}
	} else {
		return nil, fmt.Errorf("kubeconfig or api_server is required")
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("create k8s clientset: %w", err)
	}

	res := &NamespaceResources{Namespace: namespace}

	podList, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list pods: %w", err)
	}
	res.Pods = len(podList.Items)

	deployList, err := clientset.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list deployments: %w", err)
	}
	res.Deployments = len(deployList.Items)

	svcList, err := clientset.CoreV1().Services(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list services: %w", err)
	}
	res.Services = len(svcList.Items)

	cmList, err := clientset.CoreV1().ConfigMaps(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list configmaps: %w", err)
	}
	res.ConfigMaps = len(cmList.Items)

	secretList, err := clientset.CoreV1().Secrets(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list secrets: %w", err)
	}
	res.Secrets = len(secretList.Items)

	pvcList, err := clientset.CoreV1().PersistentVolumeClaims(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list pvcs: %w", err)
	}
	res.PVCs = len(pvcList.Items)

	saList, err := clientset.CoreV1().ServiceAccounts(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list serviceaccounts: %w", err)
	}
	res.ServiceAccounts = len(saList.Items)

	return res, nil
}

func getNodeRoles(node *corev1.Node) []string {
	roles := []string{}
	for label := range node.Labels {
		if label == "node-role.kubernetes.io/control-plane" || label == "node-role.kubernetes.io/master" {
			roles = append(roles, "control-plane")
		}
	}
	if len(roles) == 0 {
		roles = append(roles, "worker")
	}
	return roles
}
