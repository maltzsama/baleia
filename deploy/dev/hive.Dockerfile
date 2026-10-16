# Extends the pinned Apache Hive image with the PostgreSQL JDBC driver so the
# metastore can use Baleia's PostgreSQL as its backend (#86). The official
# image ships no JDBC driver: its Dockerfile removes apache-hive-*/jdbc/*.
# The driver is fetched in a throwaway stage with a current Alpine, so the
# Hive image's pinned/deprecated base is never touched by a package manager.
ARG PG_JDBC_VERSION=42.7.4

FROM alpine:3.20 AS driver
ARG PG_JDBC_VERSION
RUN apk add --no-cache wget \
 && wget -qO /postgresql.jar "https://jdbc.postgresql.org/download/postgresql-${PG_JDBC_VERSION}.jar"

FROM apache/hive:4.0.1
USER root
COPY --from=driver /postgresql.jar /opt/hive/lib/postgresql.jar
USER hive
