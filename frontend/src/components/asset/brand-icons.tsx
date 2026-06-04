import { Icon, type IconifyIcon } from "@iconify/react";

// Offline icon data imports (bundled at build time, no CDN)
// logos set: multi-colored official brand icons
import awsIcon from "@iconify-icons/logos/aws";
import azureIcon from "@iconify-icons/logos/microsoft-azure";
import gcpIcon from "@iconify-icons/logos/google-cloud";
import cloudflareIcon from "@iconify-icons/logos/cloudflare-icon";
import mysqlIcon from "@iconify-icons/logos/mysql-icon";
import postgresqlIcon from "@iconify-icons/logos/postgresql";
import redisIcon from "@iconify-icons/logos/redis";
import mongodbIcon from "@iconify-icons/logos/mongodb-icon";
import elasticsearchIcon from "@iconify-icons/logos/elasticsearch";
import mariadbIcon from "@iconify-icons/logos/mariadb-icon";
import rabbitmqIcon from "@iconify-icons/logos/rabbitmq-icon";
import etcdIcon from "@iconify-icons/logos/etcd";
import dockerIcon from "@iconify-icons/logos/docker-icon";
import kubernetesIcon from "@iconify-icons/logos/kubernetes";
import windowsIcon from "@iconify-icons/logos/microsoft-windows-icon";
import ubuntuIcon from "@iconify-icons/logos/ubuntu";
import centosIcon from "@iconify-icons/logos/centos-icon";
import debianIcon from "@iconify-icons/logos/debian";
import redhatIcon from "@iconify-icons/logos/redhat-icon";
import nginxIcon from "@iconify-icons/logos/nginx";
import grafanaIcon from "@iconify-icons/logos/grafana";
import prometheusIcon from "@iconify-icons/logos/prometheus";
import digitalOceanIcon from "@iconify-icons/logos/digital-ocean-icon";
import linodeIcon from "@iconify-icons/logos/linode";
import vultrIcon from "@iconify-icons/logos/vultr-icon";
import openstackIcon from "@iconify-icons/logos/openstack-icon";
import oracleIcon from "@iconify-icons/logos/oracle";
import cassandraIcon from "@iconify-icons/logos/cassandra";
import neo4jIcon from "@iconify-icons/logos/neo4j";
import natsIcon from "@iconify-icons/logos/nats-icon";
import memcachedIcon from "@iconify-icons/logos/memcached";
import fedoraIcon from "@iconify-icons/logos/fedora";
import archIcon from "@iconify-icons/logos/archlinux";
import freebsdIcon from "@iconify-icons/logos/freebsd";
import jenkinsIcon from "@iconify-icons/logos/jenkins";
import gitlabIcon from "@iconify-icons/logos/gitlab";
import terraformIcon from "@iconify-icons/logos/terraform-icon";
import apacheIcon from "@iconify-icons/logos/apache";
import consulIcon from "@iconify-icons/logos/consul";
import datadogIcon from "@iconify-icons/logos/datadog-icon";
import kibanaIcon from "@iconify-icons/logos/kibana";
import argoIcon from "@iconify-icons/logos/argo";
import rancherIcon from "@iconify-icons/logos/rancher";
// simple-icons set: monochrome brand icons (for providers not in logos)
import alicloudIcon from "@iconify-icons/simple-icons/alibabacloud";
import huaweiIcon from "@iconify-icons/simple-icons/huawei";
import clickhouseIcon from "@iconify-icons/simple-icons/clickhouse";
import kafkaIcon from "@iconify-icons/simple-icons/apachekafka";
import sqliteIcon from "@iconify-icons/simple-icons/sqlite";
import appleIcon from "@iconify-icons/simple-icons/apple";
import linuxIcon from "@iconify-icons/simple-icons/linux";
import ibmIcon from "@iconify-icons/simple-icons/ibm";
import herokuIcon from "@iconify-icons/simple-icons/heroku";
import sqlserverIcon from "@iconify-icons/simple-icons/microsoftsqlserver";
import influxdbIcon from "@iconify-icons/simple-icons/influxdb";
import cockroachdbIcon from "@iconify-icons/simple-icons/cockroachlabs";
import minioIcon from "@iconify-icons/simple-icons/minio";
import pulsarIcon from "@iconify-icons/simple-icons/apachepulsar";
import alpineIcon from "@iconify-icons/simple-icons/alpinelinux";
import opensuseIcon from "@iconify-icons/simple-icons/opensuse";
import rockyIcon from "@iconify-icons/simple-icons/rockylinux";
import podmanIcon from "@iconify-icons/simple-icons/podman";
import containerdIcon from "@iconify-icons/simple-icons/containerd";
import traefikIcon from "@iconify-icons/simple-icons/traefikproxy";
import istioIcon from "@iconify-icons/simple-icons/istio";
import harborIcon from "@iconify-icons/simple-icons/harbor";
// tdesign set: Tencent's own design system (no Tencent Cloud logo in any icon set)
import tencentCloudIcon from "@iconify-icons/tdesign/cloud";

interface IconProps {
  className?: string;
  style?: React.CSSProperties;
}

function brandIcon(data: IconifyIcon | string) {
  const Component: React.FC<IconProps> = ({ className, style }) => (
    <Icon icon={data} className={className} style={style} />
  );
  return Component;
}

// ===== Cloud Providers =====
export const AwsIcon = brandIcon(awsIcon);
export const AzureIcon = brandIcon(azureIcon);
export const GcpIcon = brandIcon(gcpIcon);
export const AliCloudIcon = brandIcon(alicloudIcon);
export const TencentCloudIcon = brandIcon(tencentCloudIcon);
export const HuaweiCloudIcon = brandIcon(huaweiIcon);
export const CloudflareIcon = brandIcon(cloudflareIcon);
export const DigitalOceanIcon = brandIcon(digitalOceanIcon);
export const IbmCloudIcon = brandIcon(ibmIcon);
export const HerokuIcon = brandIcon(herokuIcon);
export const LinodeIcon = brandIcon(linodeIcon);
export const VultrIcon = brandIcon(vultrIcon);
export const OpenstackIcon = brandIcon(openstackIcon);

// ===== Databases & Middleware =====
export const MysqlIcon = brandIcon(mysqlIcon);
export const PostgresqlIcon = brandIcon(postgresqlIcon);
export const RedisIcon = brandIcon(redisIcon);
export const MongodbIcon = brandIcon(mongodbIcon);
export const ElasticsearchIcon = brandIcon(elasticsearchIcon);
export const KafkaIcon = brandIcon(kafkaIcon);
export const MariadbIcon = brandIcon(mariadbIcon);
export const SqliteIcon = brandIcon(sqliteIcon);
export const RabbitmqIcon = brandIcon(rabbitmqIcon);
export const EtcdIcon = brandIcon(etcdIcon);
export const ClickhouseIcon = brandIcon(clickhouseIcon);
export const SqlserverIcon = brandIcon(sqlserverIcon);
export const OracleIcon = brandIcon(oracleIcon);
export const CassandraIcon = brandIcon(cassandraIcon);
export const Neo4jIcon = brandIcon(neo4jIcon);
export const InfluxdbIcon = brandIcon(influxdbIcon);
export const CockroachdbIcon = brandIcon(cockroachdbIcon);
export const MinioIcon = brandIcon(minioIcon);
export const NatsIcon = brandIcon(natsIcon);
export const PulsarIcon = brandIcon(pulsarIcon);
export const MemcachedIcon = brandIcon(memcachedIcon);

// ===== System / OS =====
export const DockerIcon = brandIcon(dockerIcon);
export const KubernetesIcon = brandIcon(kubernetesIcon);
export const LinuxIcon = brandIcon(linuxIcon);
export const WindowsIcon = brandIcon(windowsIcon);
export const UbuntuIcon = brandIcon(ubuntuIcon);
export const CentosIcon = brandIcon(centosIcon);
export const DebianIcon = brandIcon(debianIcon);
export const RedhatIcon = brandIcon(redhatIcon);
export const MacosIcon = brandIcon(appleIcon);
export const AlpineIcon = brandIcon(alpineIcon);
export const FedoraIcon = brandIcon(fedoraIcon);
export const ArchIcon = brandIcon(archIcon);
export const FreebsdIcon = brandIcon(freebsdIcon);
export const OpensuseIcon = brandIcon(opensuseIcon);
export const RockyIcon = brandIcon(rockyIcon);
export const PodmanIcon = brandIcon(podmanIcon);
export const ContainerdIcon = brandIcon(containerdIcon);

// ===== DevOps & Monitoring =====
export const NginxIcon = brandIcon(nginxIcon);
export const GrafanaIcon = brandIcon(grafanaIcon);
export const PrometheusIcon = brandIcon(prometheusIcon);
export const JenkinsIcon = brandIcon(jenkinsIcon);
export const GitlabIcon = brandIcon(gitlabIcon);
export const TerraformIcon = brandIcon(terraformIcon);
export const ApacheIcon = brandIcon(apacheIcon);
export const ConsulIcon = brandIcon(consulIcon);
export const DatadogIcon = brandIcon(datadogIcon);
export const KibanaIcon = brandIcon(kibanaIcon);
export const TraefikIcon = brandIcon(traefikIcon);
export const IstioIcon = brandIcon(istioIcon);
export const ArgoIcon = brandIcon(argoIcon);
export const RancherIcon = brandIcon(rancherIcon);
export const HarborIcon = brandIcon(harborIcon);
