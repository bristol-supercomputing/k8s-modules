FROM docker.io/library/debian:bookworm AS build
RUN apt-get update && apt-get install -y --no-install-recommends gcc libc6-dev && rm -rf /var/lib/apt/lists/*
COPY src/ /src/
RUN sh /src/build.sh

FROM scratch AS host-libs
COPY --from=build /out/host/ /

FROM docker.io/library/debian:bookworm-slim AS client
COPY --from=build /out/client/ /
RUN ldconfig

FROM docker.io/library/debian:buster-slim AS client-old
COPY --from=build /out/client/ /
RUN ldconfig
