# SPDX-License-Identifier: Apache-2.0
#
# A clean room for docs/THIRD-PARTY-VERIFIER.md: Debian with git, curl and Go,
# and nothing else. It copies in run.sh only; run.sh clones evidencebook and
# downloads every other input from its public URL.
#
#   docker build -t eb-cleanroom test/cleanroom
#   docker run --rm eb-cleanroom
#   docker run --rm -e EB_REF=<commit> eb-cleanroom
FROM debian:bookworm-slim
ARG GO_VERSION=1.27.0
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl git \
 && rm -rf /var/lib/apt/lists/*
RUN arch=$(dpkg --print-architecture) \
 && curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${arch}.tar.gz" | tar -C /usr/local -xz
RUN useradd -m tester
USER tester
ENV PATH=/usr/local/go/bin:$PATH
WORKDIR /home/tester
COPY --chown=tester run.sh /usr/local/bin/eb-cleanroom
CMD ["eb-cleanroom"]
