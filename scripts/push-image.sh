#!/bin/bash
set -e
set -u

env >> BASH_ENV
cat BASH_ENV | while read line; do
	export $line
done
if [ -z "${CIRCLE_TAG:-}" ]; then
  echo "Pushing latest for $CIRCLE_BRANCH..."
  TAG=latest
else
  echo "Pushing release $CIRCLE_TAG..."
  TAG="$CIRCLE_TAG"
fi
echo $DOCKER_PASSWORD | docker login --username $DOCKER_USERNAME --password-stdin
make build-image-with-tag

rm BASH_ENV
