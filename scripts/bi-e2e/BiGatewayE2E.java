import java.io.IOException;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.sql.Connection;
import java.sql.DriverManager;
import java.sql.ResultSet;
import java.sql.SQLException;
import java.sql.Statement;
import java.time.Duration;
import java.util.Base64;
import java.util.Properties;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.Future;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.TimeoutException;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

/**
 * End-to-end check of the BI gateway (ADR 0017) with the Trino JDBC driver —
 * the reference Trino-protocol client, since Tableau and Power BI speak the
 * same protocol.
 *
 * <p>Run from {@code make e2e}, after cmd/e2e-seed wrote the BI key secret and
 * kubectl forwards the gateway Service to localhost:8443. The checks are:
 *
 * <ol>
 *   <li>HTTP Basic with a workspace-bound BI key, a query, and a multi-page
 *       result (each nextUri is followed through the gateway);
 *   <li>a nextUri signed by the gateway validates on every API replica, not
 *       only the one that served the first page (BI_E2E_PODS);
 *   <li>cancel of a running statement, and the connection still works after;
 *   <li>a catalog outside the workspace scope is denied;
 *   <li>/v1/info answers, an unauthenticated request is 401, any other path is
 *       404, and a spooling-capable request still gets inline pages because
 *       the gateway drops the encoding header.
 * </ol>
 *
 * It needs the trino-jdbc jar on the class path (see the Makefile target).
 */
public final class BiGatewayE2E {
    private static final Pattern NEXT_URI = Pattern.compile("\"nextUri\"\\s*:\\s*\"([^\"]+)\"");
    private static final Pattern STATE = Pattern.compile("\"state\"\\s*:\\s*\"([A-Z]+)\"");
    // The e2e terminates TLS itself (tlsproxy.go), so trust its certificate.
    private static final HttpClient HTTP = trustAllClient();

    private static String base;
    private static String user;
    private static String secret;
    private static String catalog;
    private static String schema;
    private static String[] pods;

    private BiGatewayE2E() {}

    public static void main(String[] args) throws Exception {
        base = env("BI_E2E_URL", "https://localhost:8444");
        user = env("BI_E2E_USER", "admin@e2e.test");
        catalog = env("BI_E2E_CATALOG", "e2e_tpch");
        schema = env("BI_E2E_SCHEMA", "tiny");
        pods = env("BI_E2E_PODS", "").isBlank() ? new String[0] : env("BI_E2E_PODS", "").split(",");
        Path keyFile = Path.of(env("BI_E2E_KEY_FILE", "/tmp/baleia-e2e-bi-key.secret"));
        secret = Files.readString(keyFile, StandardCharsets.UTF_8).trim();
        if (secret.isEmpty()) {
            throw new IllegalStateException("empty BI key in " + keyFile);
        }

        String authority = URI.create(base).getAuthority();
        String jdbc = "jdbc:trino://" + authority + "/" + catalog + "/" + schema;
        Properties props = new Properties();
        props.setProperty("user", user);
        props.setProperty("password", secret);
        boolean secure = base.startsWith("https://");
        props.setProperty("SSL", String.valueOf(secure));
        if (secure) {
            // tlsproxy.go uses a self-signed certificate; a real edge has one
            // the clients trust.
            props.setProperty("SSLVerification", "NONE");
        }
        props.setProperty("source", "baleia-e2e");
        props.setProperty("clientTags", "e2e");

        try (Connection conn = DriverManager.getConnection(jdbc, props)) {
            check("Basic auth and a statement", () -> {
                try (Statement st = conn.createStatement();
                        ResultSet rs = st.executeQuery("SELECT 1")) {
                    rs.next();
                    int one = rs.getInt(1);
                    if (one != 1) {
                        throw new AssertionError("SELECT 1 = " + one);
                    }
                }
            });

            check("a multi-page result follows the gateway URIs", () -> {
                long rows = 0;
                try (Statement st = conn.createStatement();
                        ResultSet rs = st.executeQuery("SELECT * FROM orders")) {
                    while (rs.next()) {
                        rows++;
                    }
                }
                if (rows != 15000) {
                    throw new AssertionError("SELECT * FROM orders returned " + rows + " rows, want 15000");
                }
            });

            check("a catalog outside the workspace scope is denied", () -> {
                try (Statement st = conn.createStatement()) {
                    st.executeQuery("SELECT count(*) FROM tpch.tiny.nation");
                    throw new AssertionError("a catalog outside the workspace scope was allowed");
                } catch (SQLException expected) {
                    String msg = String.valueOf(expected.getMessage()).toLowerCase();
                    if (!msg.contains("access denied") && !msg.contains("permission")
                            && !msg.contains("not allowed") && !msg.contains("tpch")) {
                        throw new AssertionError(
                                "the scope denial does not name a permission error: " + expected.getMessage());
                    }
                }
            });

            check("a running statement is cancelled", () -> {
                ExecutorService pool = Executors.newSingleThreadExecutor();
                try (Statement st = conn.createStatement()) {
                    Future<?> running = pool.submit(() -> {
                        try (ResultSet rs = st.executeQuery("SELECT count(*) FROM orders o1 CROSS JOIN orders o2")) {
                            while (rs.next()) {
                                // Not expected to finish; cancelling is the test.
                            }
                        } catch (SQLException cancelled) {
                            // The driver reports the cancellation.
                        }
                    });
                    Thread.sleep(2000);
                    st.cancel();
                    try {
                        running.get(30, TimeUnit.SECONDS);
                    } catch (TimeoutException e) {
                        throw new AssertionError("the cancelled statement kept running");
                    }
                } finally {
                    pool.shutdownNow();
                }
                try (Statement st = conn.createStatement();
                        ResultSet rs = st.executeQuery("SELECT 1")) {
                    rs.next();
                }
            });
        }

        check("GET /v1/info answers with the key", () -> {
            HttpResponse<String> r = send(request(base + "/v1/info").GET());
            if (r.statusCode() != 200 || !r.body().contains("nodeVersion")) {
                throw new AssertionError("/v1/info = " + r.statusCode() + " " + r.body());
            }
        });

        check("a request without credentials is 401", () -> {
            HttpRequest anonymous = HttpRequest.newBuilder(URI.create(base + "/v1/info"))
                    .timeout(Duration.ofSeconds(30))
                    .GET()
                    .build();
            HttpResponse<String> r = HTTP.send(anonymous, HttpResponse.BodyHandlers.ofString());
            if (r.statusCode() != 401) {
                throw new AssertionError("unauthenticated /v1/info = " + r.statusCode() + ", want 401");
            }
        });

        check("any other path is 404", () -> {
            HttpResponse<String> r = send(request(base + "/v1/not-a-trino-endpoint").GET());
            if (r.statusCode() != 404) {
                throw new AssertionError("unknown path = " + r.statusCode() + ", want 404");
            }
        });

        check("a spooling-capable request still gets inline pages", () -> {
            HttpResponse<String> r = send(request(base + "/v1/statement")
                    .header("X-Trino-Query-Data-Encoding", "json+zstd")
                    .POST(HttpRequest.BodyPublishers.ofString("SELECT 1")));
            for (int page = 0; page < 50; page++) {
                if (r.statusCode() != 200) {
                    throw new AssertionError("statement = " + r.statusCode() + " " + r.body());
                }
                if (r.body().contains("\"segments\"")) {
                    throw new AssertionError("the gateway returned a spooled segment: " + r.body());
                }
                String next = nextUri(r.body());
                if (next == null || isFinished(r.body())) {
                    return;
                }
                r = send(request(next).GET());
            }
            throw new AssertionError("the statement did not finish");
        });

        check("a nextUri validates on every API replica", () -> {
            if (pods.length < 2) {
                throw new AssertionError("BI_E2E_PODS needs both API replicas, got " + pods.length);
            }
            for (String pod : pods) {
                HttpResponse<String> first = send(request(base + "/v1/statement")
                        .POST(HttpRequest.BodyPublishers.ofString("SELECT count(*) FROM orders")));
                if (first.statusCode() != 200) {
                    throw new AssertionError("statement = " + first.statusCode() + " " + first.body());
                }
                String next = nextUri(first.body());
                if (next == null) {
                    continue; // finished on the first page; nothing to follow
                }
                URI uri = URI.create(next);
                URI direct = URI.create(pod + uri.getRawPath() + "?" + uri.getRawQuery());
                HttpResponse<String> followed = send(request(direct.toString()).GET());
                if (followed.statusCode() != 200) {
                    throw new AssertionError("replica " + pod + " refused a nextUri signed elsewhere: "
                            + followed.statusCode() + " " + followed.body());
                }
                // Close the execution through the gateway so it does not stay running.
                String next2 = nextUri(followed.body());
                if (next2 != null) {
                    send(request(next2).DELETE());
                }
            }
        });

        System.out.println("BI GATEWAY E2E PASS (" + base + ", " + user + ", " + catalog + "." + schema + ", "
                + pods.length + " replicas)");
    }

    private static void check(String name, Checked body) throws Exception {
        body.run();
        System.out.println("ok    " + name);
    }

    private interface Checked {
        void run() throws Exception;
    }

    private static HttpRequest.Builder request(String uri) {
        String auth = "Basic " + Base64.getEncoder()
                .encodeToString((user + ":" + secret).getBytes(StandardCharsets.UTF_8));
        return HttpRequest.newBuilder(URI.create(uri))
                .timeout(Duration.ofSeconds(60))
                .header("Authorization", auth);
    }

    private static HttpResponse<String> send(HttpRequest.Builder builder) throws IOException, InterruptedException {
        return HTTP.send(builder.build(), HttpResponse.BodyHandlers.ofString());
    }

    private static String nextUri(String body) {
        Matcher m = NEXT_URI.matcher(body);
        return m.find() ? m.group(1).replace("\\/", "/").replace("\\u0026", "&") : null;
    }

    private static boolean isFinished(String body) {
        Matcher m = STATE.matcher(body);
        return m.find() && ("FINISHED".equals(m.group(1)) || "FAILED".equals(m.group(1))
                || "CANCELED".equals(m.group(1)));
    }

    private static HttpClient trustAllClient() {
        try {
            javax.net.ssl.TrustManager[] trustAll = {
                new javax.net.ssl.X509TrustManager() {
                    public void checkClientTrusted(java.security.cert.X509Certificate[] chain, String authType) {}

                    public void checkServerTrusted(java.security.cert.X509Certificate[] chain, String authType) {}

                    public java.security.cert.X509Certificate[] getAcceptedIssuers() {
                        return new java.security.cert.X509Certificate[0];
                    }
                }
            };
            javax.net.ssl.SSLContext context = javax.net.ssl.SSLContext.getInstance("TLS");
            context.init(null, trustAll, new java.security.SecureRandom());
            return HttpClient.newBuilder()
                    .sslContext(context)
                    .connectTimeout(Duration.ofSeconds(10))
                    .build();
        } catch (Exception e) {
            throw new IllegalStateException("could not build the e2e HTTP client", e);
        }
    }

    private static String env(String name, String fallback) {
        String value = System.getenv(name);
        return value == null || value.isBlank() ? fallback : value;
    }
}
