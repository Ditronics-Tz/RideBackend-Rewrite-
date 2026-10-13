FROM postgres:16-bookworm

USER root
RUN apt-get update \
    && apt-get install -y --no-install-recommends openssl \
    && rm -rf /var/lib/apt/lists/*

COPY docker/postgres-entrypoint.sh /usr/local/bin/rida-postgres-entrypoint
RUN chmod 0755 /usr/local/bin/rida-postgres-entrypoint
COPY docker/init-app-role.sh /docker-entrypoint-initdb.d/10-app-role.sh
RUN chmod 0755 /docker-entrypoint-initdb.d/10-app-role.sh

ENTRYPOINT ["/usr/local/bin/rida-postgres-entrypoint"]
CMD ["postgres"]
