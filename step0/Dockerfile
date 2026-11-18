# Step-0 sandbox probe: proves ioi/isolate works inside a privileged container
# on ARM64 Ubuntu with cgroup v2, and that AC/WA/TLE/MLE/RE/CE all come out.
#
# HOST PRECONDITIONS
#   * cgroup v2:  stat -fc %T /sys/fs/cgroup   ->  cgroup2fs
#   * container MUST run with --privileged (isolate needs CAP_SYS_ADMIN to
#     mount the box chroot and to create/delegate cgroups), e.g.
#       docker run --rm --privileged --cgroupns=host \
#         -v /sys/fs/cgroup:/sys/fs/cgroup:rw lnd4su-step0
FROM debian:bookworm

RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      git gcc g++ make pkg-config libcap-dev libseccomp-dev ca-certificates coreutils \
 && rm -rf /var/lib/apt/lists/*

# Build isolate from source. This git clone is the only network access the
# image build performs; the probe itself downloads nothing at runtime.
RUN git clone --depth 1 https://github.com/ioi/isolate /tmp/isolate \
 && make -C /tmp/isolate isolate \
 && install -o root -g root -m 4755 /tmp/isolate/isolate /usr/local/bin/isolate \
 && rm -rf /tmp/isolate

COPY probe.sh /probe/probe.sh
COPY cases/ /probe/cases/

WORKDIR /probe
ENTRYPOINT ["/bin/bash","/probe/probe.sh"]
