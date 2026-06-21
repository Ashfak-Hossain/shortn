variable "cluster_name" {
  description = "Name of the kind cluster."
  type        = string
  default     = "shortn"
}

variable "argocd_chart_version" {
  description = "argo-cd Helm chart version (https://github.com/argoproj/argo-helm/releases)."
  type        = string
  default     = "7.7.11"
}
