# Real OpenHands V1 agent-server image, retagged for use as the test
# agent runtime in the executor_docker_opehands autotest.
#
# In environments without internet access, pre-pull the upstream image
# and rebuild with --build-arg AGENT_SERVER_IMAGE=<local-mirror>.
ARG AGENT_SERVER_IMAGE=ghcr.io/openhands/agent-server:latest-python
FROM ${AGENT_SERVER_IMAGE}

LABEL org.opencontainers.image.source="https://github.com/OpenHands/software-agent-sdk" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.title="flowai-agent-openhands-image" \
      org.opencontainers.image.description="Real OpenHands V1 agent-server for executor_docker_opehands e2e"
