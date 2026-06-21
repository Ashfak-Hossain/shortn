output "cluster_name" {
  description = "The kind cluster Terraform created."
  value       = kind_cluster.shortn.name
}

output "kubeconfig_path" {
  description = "Path to the kubeconfig kind wrote for this cluster."
  value       = kind_cluster.shortn.kubeconfig_path
}