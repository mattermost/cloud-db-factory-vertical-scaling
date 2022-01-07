set -e
set -u
export TAG="${CIRCLE_SHA1:0:7}"
echo $(DOCKER_PASSWORD) | docker login --username $(DOCKER_USERNAME) --password-stdin
docker tag mattermost/cloud-db-factory-vertical-scaling:test mattermost/cloud-db-factory-vertical-scaling:$(TAG)
docker push mattermost/cloud-db-factory-vertical-scaling:$(TAG)
