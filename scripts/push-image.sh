set -e
set -u
if [[ -z "${CIRCLE_TAG:-}" ]]; then
  echo "Pushing latest for $(CIRCLE_BRANCH)..."
  TAG=latest
else
  echo "Pushing release $(CIRCLE_TAG)..."
  TAG="$CIRCLE_TAG"
fi
echo $(DOCKER_PASSWORD) | docker login --username $(DOCKER_USERNAME) --password-stdin
docker tag mattermost/cloud-db-factory-vertical-scaling:test mattermost/cloud-db-factory-vertical-scaling:$(TAG)
docker push mattermost/cloud-db-factory-vertical-scaling:$(TAG)
