#!/bin/bash

##############################################################################
# install-primary-remote.sh
#
# Installs Istio across two clusters using the "primary-remote" model.
#
# See: https://istio.io/latest/docs/setup/install/multicluster/primary-remote/
#
# See --help for more details on options to this script.
#
##############################################################################

SCRIPT_DIR="$(cd $(dirname "${BASH_SOURCE[0]}") && pwd)"
source ${SCRIPT_DIR}/env.sh $*

echo "Starting minikube instances"
source ${SCRIPT_DIR}/start-minikube.sh

# Setup the certificates
source ${SCRIPT_DIR}/setup-ca.sh

# Cluster East
switch_cluster "${CLUSTER1_CONTEXT}" "${CLUSTER1_USER}" "${CLUSTER1_PASS}"

${CLIENT_EXE} label namespace ${ISTIO_NAMESPACE} topology.istio.io/network=${NETWORK1_ID}

ISTIO_INSTALL_SCRIPT="${SCRIPT_DIR}/../install-istio-via-istioctl.sh"
${ISTIO_INSTALL_SCRIPT} --client-exe-path ${CLIENT_EXE} --cluster-name ${CLUSTER1_NAME} --istioctl ${ISTIOCTL} --istio-dir ${ISTIO_DIR} --mesh-id ${MESH_ID} --namespace ${ISTIO_NAMESPACE} --network ${NETWORK1_ID} --set values.pilot.env.EXTERNAL_ISTIOD=true 

GEN_GATEWAY_SCRIPT="${ISTIO_DIR}/samples/multicluster/gen-eastwest-gateway.sh"
${GEN_GATEWAY_SCRIPT} --mesh ${MESH_ID} --cluster ${CLUSTER1_NAME} --network ${NETWORK1_ID} | ${ISTIOCTL} --context=${CLUSTER1_CONTEXT} install -y -f -

EXPOSE_ISTIOD_YAML="${ISTIO_DIR}/samples/multicluster/expose-istiod.yaml"
${CLIENT_EXE} apply --context=${CLUSTER1_CONTEXT} -n ${ISTIO_NAMESPACE} -f $EXPOSE_ISTIOD_YAML

EXPOSE_SERVICES_YAML="${ISTIO_DIR}/samples/multicluster/expose-services.yaml"
${CLIENT_EXE} apply --context=${CLUSTER1_CONTEXT} -n ${ISTIO_NAMESPACE} -f $EXPOSE_SERVICES_YAML

# Install Kiali in both clusters if enabled
source ${SCRIPT_DIR}/deploy-kiali.sh

# Cluster West
switch_cluster "${CLUSTER2_CONTEXT}" "${CLUSTER2_USER}" "${CLUSTER2_PASS}"

${CLIENT_EXE} --context=${CLUSTER2_CONTEXT} annotate namespace ${ISTIO_NAMESPACE} topology.istio.io/controlPlaneClusters=${CLUSTER1_NAME}
${CLIENT_EXE} --context=${CLUSTER2_CONTEXT} label namespace ${ISTIO_NAMESPACE} topology.istio.io/network=${NETWORK2_ID}

DISCOVERY_ADDRESS=$(${CLIENT_EXE} --context=${CLUSTER1_CONTEXT} -n ${ISTIO_NAMESPACE} get svc istio-eastwestgateway -o jsonpath='{.status.loadBalancer.ingress[0].ip}')

${ISTIOCTL} install -y --force=true --set profile=remote --set values.istiodRemote.injectionPath=/inject/cluster/${CLUSTER2_NAME}/net/${NETWORK2_ID} --set values.global.remotePilotAddress=${DISCOVERY_ADDRESS}
${CLIENT_EXE} apply -f ${ISTIO_DIR}/samples/addons/prometheus.yaml -n ${ISTIO_NAMESPACE}

CA_BUNDLE=$(${CLIENT_EXE} get secret cacerts -n ${ISTIO_NAMESPACE} --context ${CLUSTER1_CONTEXT} -o jsonpath={.data."ca-cert\.pem"})

${CLIENT_EXE} patch mutatingwebhookconfigurations.admissionregistration.k8s.io -n ${ISTIO_NAMESPACE} istio-sidecar-injector -p "{\"webhooks\":[{\"clientConfig\":{\"caBundle\":\"${CA_BUNDLE}\"},\"name\":\"rev.namespace.sidecar-injector.istio.io\"}]}"
${CLIENT_EXE} patch mutatingwebhookconfigurations.admissionregistration.k8s.io -n ${ISTIO_NAMESPACE} istio-sidecar-injector -p "{\"webhooks\":[{\"clientConfig\":{\"caBundle\":\"${CA_BUNDLE}\"},\"name\":\"rev.object.sidecar-injector.istio.io\"}]}"
${CLIENT_EXE} patch mutatingwebhookconfigurations.admissionregistration.k8s.io -n ${ISTIO_NAMESPACE} istio-sidecar-injector -p "{\"webhooks\":[{\"clientConfig\":{\"caBundle\":\"${CA_BUNDLE}\"},\"name\":\"namespace.sidecar-injector.istio.io\"}]}"
${CLIENT_EXE} patch mutatingwebhookconfigurations.admissionregistration.k8s.io -n ${ISTIO_NAMESPACE} istio-sidecar-injector -p "{\"webhooks\":[{\"clientConfig\":{\"caBundle\":\"${CA_BUNDLE}\"},\"name\":\"object.sidecar-injector.istio.io\"}]}"

${CLIENT_EXE} patch mutatingwebhookconfigurations.admissionregistration.k8s.io -n ${ISTIO_NAMESPACE} istio-revision-tag-default -p "{\"webhooks\":[{\"clientConfig\":{\"caBundle\":\"${CA_BUNDLE}\"},\"name\":\"rev.namespace.sidecar-injector.istio.io\"}]}"
${CLIENT_EXE} patch mutatingwebhookconfigurations.admissionregistration.k8s.io -n ${ISTIO_NAMESPACE} istio-revision-tag-default -p "{\"webhooks\":[{\"clientConfig\":{\"caBundle\":\"${CA_BUNDLE}\"},\"name\":\"rev.object.sidecar-injector.istio.io\"}]}"
${CLIENT_EXE} patch mutatingwebhookconfigurations.admissionregistration.k8s.io -n ${ISTIO_NAMESPACE} istio-revision-tag-default -p "{\"webhooks\":[{\"clientConfig\":{\"caBundle\":\"${CA_BUNDLE}\"},\"name\":\"namespace.sidecar-injector.istio.io\"}]}"
${CLIENT_EXE} patch mutatingwebhookconfigurations.admissionregistration.k8s.io -n ${ISTIO_NAMESPACE} istio-revision-tag-default -p "{\"webhooks\":[{\"clientConfig\":{\"caBundle\":\"${CA_BUNDLE}\"},\"name\":\"object.sidecar-injector.istio.io\"}]}"

${ISTIOCTL} x create-remote-secret --context=${CLUSTER2_CONTEXT} --name=${CLUSTER2_NAME} | ${CLIENT_EXE} apply -f - --context="${CLUSTER1_CONTEXT}"

${GEN_GATEWAY_SCRIPT} --mesh ${MESH_ID} --cluster ${CLUSTER2_NAME} --network ${NETWORK2_ID} | ${ISTIOCTL} --context=${CLUSTER2_CONTEXT} install -y -f -

# Only configure Prometheus federation when one "unified" Kiali is deployed.
if [ "${SINGLE_KIALI}" == "true" ]; then
  ${CLIENT_EXE} patch svc prometheus -n ${ISTIO_NAMESPACE} --context ${CLUSTER2_CONTEXT} -p "{\"spec\": {\"type\": \"LoadBalancer\"}}"

  WEST_PROMETHEUS_ADDRESS=$(${CLIENT_EXE} --context=${CLUSTER2_CONTEXT} -n ${ISTIO_NAMESPACE} get svc prometheus -o jsonpath='{.status.loadBalancer.ingress[0].ip}')
  sed -i "s/WEST_PROMETHEUS_ADDRESS/$WEST_PROMETHEUS_ADDRESS/g" ${SCRIPT_DIR}/prometheus.yaml
  ${CLIENT_EXE} apply -f ${SCRIPT_DIR}/prometheus.yaml -n ${ISTIO_NAMESPACE} --context ${CLUSTER1_CONTEXT} 
  sed -i "s/$WEST_PROMETHEUS_ADDRESS/WEST_PROMETHEUS_ADDRESS/g" ${SCRIPT_DIR}/prometheus.yaml
fi

# Install bookinfo across cluster if enabled
source ${SCRIPT_DIR}/split-bookinfo.sh

