package com.wails.app;

import android.util.Log;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import android.webkit.JavascriptInterface;
import android.webkit.WebView;
// BuildConfig is generated under the AGP namespace (com.codeneow.llamadesktop),
// which differs from this file's Java package (com.wails.app — required by the
// Wails v3 Go runtime's hardcoded JNI export names).
import com.codeneow.llamadesktop.BuildConfig;

/**
 * WailsJSBridge provides the JavaScript interface that allows the web frontend
 * to communicate with the Go backend. This is exposed to JavaScript as the
 * `window.wails` object.
 *
 * Similar to iOS's WKScriptMessageHandler but using Android's addJavascriptInterface.
 */
public class WailsJSBridge {
    private static final String TAG = "WailsJSBridge";
    private static final boolean DEBUG = BuildConfig.DEBUG;
    // Pooled threads avoid unbounded thread creation under high call volume.
    private static final ExecutorService executor = Executors.newCachedThreadPool();

    private final WailsBridge bridge;
    private final WebView webView;

    public WailsJSBridge(WailsBridge bridge, WebView webView) {
        this.bridge = bridge;
        this.webView = webView;
    }

    /**
     * Send a message to Go and return the response synchronously.
     * Called from JavaScript: wails.invoke(message)
     *
     * @param message The message to send (JSON string)
     * @return The response from Go (JSON string)
     */
    @JavascriptInterface
    public String invoke(String message) {
        if (DEBUG) Log.d(TAG, "Invoke called: " + message);
        return bridge.handleMessage(message);
    }

    /**
     * Send a message to Go asynchronously.
     * The response will be sent back via a callback.
     * Called from JavaScript: wails.invokeAsync(callbackId, message)
     *
     * @param callbackId The callback ID to use for the response
     * @param message The message to send (JSON string)
     */
    @JavascriptInterface
    public void invokeAsync(final String callbackId, final String payload) {
        if (DEBUG) Log.d(TAG, "InvokeAsync called: " + payload);

        // Handle off the JS thread so we don't block the WebView.
        executor.execute(() -> {
            try {
                String response = bridge.handleRuntimeCall(payload);
                sendCallback(callbackId, response, null);
            } catch (Exception e) {
                Log.e(TAG, "Error in async invoke", e);
                sendCallback(callbackId, null, e.getMessage());
            }
        });
    }

    /**
     * Log a message from JavaScript to Android's logcat
     * Called from JavaScript: wails.log(level, message)
     *
     * @param level The log level (debug, info, warn, error)
     * @param message The message to log
     */
    @JavascriptInterface
    public void log(String level, String message) {
        switch (level.toLowerCase()) {
            case "debug":
                Log.d(TAG + "/JS", message);
                break;
            case "info":
                Log.i(TAG + "/JS", message);
                break;
            case "warn":
                Log.w(TAG + "/JS", message);
                break;
            case "error":
                Log.e(TAG + "/JS", message);
                break;
            default:
                Log.v(TAG + "/JS", message);
                break;
        }
    }

    /**
     * Trigger the system package installer for a downloaded update APK. The
     * Go sandbox cannot fire Android intents, so the frontend calls this
     * after the Go backend finishes the update download.
     * Called from JavaScript: wails.installUpdateApk(path)
     *
     * @param path Absolute path of the downloaded update APK
     * @return JSON result: {"ok":true} on commit, or
     *         {"ok":false,"error":...,"code":"needInstallPermission"} when
     *         the "install unknown apps" grant is missing
     */
    @JavascriptInterface
    public String installUpdateApk(String path) {
        return bridge.installUpdateApk(path);
    }

    /**
     * Open the system Settings screen granting this app the "install unknown
     * apps" permission. Recovery action for installUpdateApk's
     * needInstallPermission result.
     * Called from JavaScript: wails.openInstallPermissionSettings()
     *
     * @return JSON result: {"ok":true} when the Settings screen was launched
     */
    @JavascriptInterface
    public String openInstallPermissionSettings() {
        return bridge.openInstallPermissionSettings();
    }

    /**
     * App info as JSON {"name","version","build","bundleId"} for the frontend
     * (WailsBridge.getAppInfoJson is otherwise only reachable from Go via
     * JNI). Named getAppInfo to avoid shadowing the WailsBridge-side name.
     * Called from JavaScript: wails.getAppInfo()
     *
     * @return App info JSON string, "{}" when the lookup fails
     */
    @JavascriptInterface
    public String getAppInfo() {
        return bridge.getAppInfoJson();
    }

    /**
     * Open the in-app QR scanner (QrScanActivity) for the LAN pairing import.
     * Fire-and-forget like installUpdateApk's "call returns immediately, the
     * outcome arrives separately" contract: the result is NOT returned here —
     * it lands later as the "common:qrscan" event, carrying
     * {"text":"<payload>"} on a hit or {"error":"cancelled"} on back-out,
     * camera-permission denial or failure.
     * Called from JavaScript: wails.startQrScan()
     */
    @JavascriptInterface
    public void startQrScan() {
        bridge.startQrScan();
    }

    /**
     * Get the platform name
     * Called from JavaScript: wails.platform()
     *
     * @return "android"
     */
    @JavascriptInterface
    public String platform() {
        return "android";
    }

    /**
     * Check if we're running in debug mode
     * Called from JavaScript: wails.isDebug()
     *
     * @return true if debug build, false otherwise
     */
    @JavascriptInterface
    public boolean isDebug() {
        return BuildConfig.DEBUG;
    }

    /**
     * Send a callback response to JavaScript
     */
    private void sendCallback(String callbackId, String result, String error) {
        final String js;
        if (error != null) {
            js = String.format(
                    "window._wailsAndroidCallback && window._wailsAndroidCallback('%s', null, '%s');",
                    escapeJsString(callbackId),
                    escapeJsString(error)
            );
        } else {
            js = String.format(
                    "window._wailsAndroidCallback && window._wailsAndroidCallback('%s', '%s', null);",
                    escapeJsString(callbackId),
                    escapeJsString(result != null ? result : "")
            );
        }

        webView.post(() -> webView.evaluateJavascript(js, null));
    }

    private String escapeJsString(String str) {
        if (str == null) return "";
        return str.replace("\\", "\\\\")
                .replace("'", "\\'")
                .replace("\n", "\\n")
                .replace("\r", "\\r")
                // JS line terminators (U+2028/U+2029) must be escaped too; built via
                // (char) casts so the Java lexer does not reinterpret them as newlines.
                .replace(String.valueOf((char) 0x2028), "\\u2028")
                .replace(String.valueOf((char) 0x2029), "\\u2029");
    }
}
