terraform {
  required_version = ">= 1.5"
  required_providers {
    kind = {
      source  = "tehcyx/kind"
      version = "~> 0.9"
    }
    helm = {
      source  = "hashicorp/helm"
      version = "~> 3.0"
    }
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = "~> 2.35"
    }
  }
}

provider "kind" {}

# The cluster itself — same shape as deploy/k8s/kind-config.yaml: the ingress-ready
# node label + host ports 80/443 so the nginx ingress controller is reachable.
resource "kind_cluster" "shortn" {
  name           = var.cluster_name
  wait_for_ready = true

  kind_config {
    kind        = "Cluster"
    api_version = "kind.x-k8s.io/v1alpha4"

    node {
      role = "control-plane"

      kubeadm_config_patches = [
        <<-EOT
        kind: InitConfiguration
        nodeRegistration:
          kubeletExtraArgs:
            node-labels: "ingress-ready=true"
        EOT
      ]

      extra_port_mappings {
        container_port = 80
        host_port      = 80
        protocol       = "TCP"
      }
      extra_port_mappings {
        container_port = 443
        host_port      = 443
        protocol       = "TCP"
      }
    }
  }
}

# Point the helm + kubernetes providers at the cluster the kind resource just created,
# using the credentials it exports (no kubeconfig file needed).
provider "helm" {
  kubernetes = {
    host                   = kind_cluster.shortn.endpoint
    client_certificate     = kind_cluster.shortn.client_certificate
    client_key             = kind_cluster.shortn.client_key
    cluster_ca_certificate = kind_cluster.shortn.cluster_ca_certificate
  }
}

provider "kubernetes" {
  host                   = kind_cluster.shortn.endpoint
  client_certificate     = kind_cluster.shortn.client_certificate
  client_key             = kind_cluster.shortn.client_key
  cluster_ca_certificate = kind_cluster.shortn.cluster_ca_certificate
}

# ArgoCD, installed from its official Helm chart (the chart version pins reproducibly).
resource "helm_release" "argocd" {
  name             = "argocd"
  namespace        = "argocd"
  create_namespace = true
  repository       = "https://argoproj.github.io/argo-helm"
  chart            = "argo-cd"
  version          = var.argocd_chart_version
}
