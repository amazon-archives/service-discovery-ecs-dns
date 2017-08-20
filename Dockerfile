FROM scratch
COPY  ecssd_agent /
ENTRYPOINT ["/ecssd_agent"]
