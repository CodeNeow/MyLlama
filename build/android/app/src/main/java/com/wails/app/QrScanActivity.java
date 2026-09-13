package com.wails.app;

import android.Manifest;
import android.content.Intent;
import android.content.pm.PackageManager;
import android.graphics.Color;
import android.graphics.Typeface;
import android.graphics.drawable.GradientDrawable;
import android.os.Bundle;
import android.util.Log;
import android.view.Gravity;
import android.view.View;
import android.view.ViewGroup;
import android.widget.FrameLayout;
import android.widget.TextView;

import androidx.annotation.NonNull;
import androidx.annotation.OptIn;
import androidx.appcompat.app.AppCompatActivity;
import androidx.camera.core.CameraSelector;
import androidx.camera.core.ExperimentalGetImage;
import androidx.camera.core.ImageAnalysis;
import androidx.camera.core.ImageProxy;
import androidx.camera.core.Preview;
import androidx.camera.lifecycle.ProcessCameraProvider;
import androidx.camera.view.PreviewView;
import androidx.core.content.ContextCompat;
import androidx.core.view.WindowCompat;

import com.google.common.util.concurrent.ListenableFuture;
import com.google.mlkit.vision.barcode.Barcode;
import com.google.mlkit.vision.barcode.BarcodeScanner;
import com.google.mlkit.vision.barcode.BarcodeScannerOptions;
import com.google.mlkit.vision.barcode.BarcodeScanning;
import com.google.mlkit.vision.common.InputImage;

import java.util.concurrent.Executor;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;

/**
 * Full-screen in-app QR scanner for the LAN pairing import (Phase 0.5): the
 * WebView has no camera path (no WebChromeClient getUserMedia), so scanning
 * runs in this native activity — a CameraX PreviewView bound to the back
 * camera plus an ML Kit barcode analyzer restricted to QR codes.
 *
 * Launched by MainActivity.launchQrScan() (after the CAMERA runtime grant).
 * The result goes back through the classic onActivityResult channel:
 * RESULT_OK with extra "text" carrying the decoded string on a hit,
 * RESULT_CANCELED on back / cancel / any failure — MainActivity turns both
 * into the "common:qrscan" event for the frontend.
 *
 * The UI is built in code on purpose: no XML layout/string resources, so the
 * Wails template res/ stays untouched. The only user-visible string reuses
 * the framework's localized android.R.string.cancel.
 */
public class QrScanActivity extends AppCompatActivity {
    private static final String TAG = "QrScanActivity";
    private static final boolean DEBUG = com.codeneow.llamadesktop.BuildConfig.DEBUG;
    /** Extra key the decoded QR text is delivered under. */
    public static final String EXTRA_TEXT = "text";

    private PreviewView previewView;
    private BarcodeScanner barcodeScanner;
    private ProcessCameraProvider cameraProvider;
    private ExecutorService cameraExecutor;
    /** Set once a QR code is accepted: later frames are dropped, finish is idempotent. */
    private boolean delivered;

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        // MainActivity only starts this activity with CAMERA granted; treat a
        // direct launch without it as a cancel so the frontend is never left
        // waiting on a result that can never come.
        if (checkSelfPermission(Manifest.permission.CAMERA) != PackageManager.PERMISSION_GRANTED) {
            setResult(RESULT_CANCELED);
            finish();
            return;
        }
        // Content below the status/navigation bars: targetSdk 28 already fits
        // system bars by default, so this is the explicit statement (API 30+)
        // that keeps the preview out of the transparent bars of Theme.WailsApp.
        WindowCompat.setDecorFitsSystemWindows(getWindow(), true);
        setContentView(buildLayout());
        bindCamera();
    }

    /** Build the full-screen preview with a top-left back arrow and a bottom cancel pill. */
    private View buildLayout() {
        float density = getResources().getDisplayMetrics().density;
        FrameLayout root = new FrameLayout(this);
        root.setBackgroundColor(Color.BLACK);

        previewView = new PreviewView(this);
        root.addView(previewView, new FrameLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT));

        // Top-start back arrow: translucent dark circle + white glyph, sized
        // for a comfortable touch target.
        TextView back = new TextView(this);
        back.setText("←");
        back.setTextSize(22f);
        back.setTypeface(Typeface.DEFAULT_BOLD);
        back.setTextColor(Color.WHITE);
        back.setGravity(Gravity.CENTER);
        back.setContentDescription(getString(android.R.string.cancel));
        GradientDrawable circle = new GradientDrawable();
        circle.setShape(GradientDrawable.OVAL);
        circle.setColor(0x66000000);
        back.setBackground(circle);
        int side = (int) (44 * density);
        int margin = (int) (16 * density);
        FrameLayout.LayoutParams backLp = new FrameLayout.LayoutParams(side, side);
        backLp.topMargin = margin;
        backLp.leftMargin = margin;
        backLp.gravity = Gravity.TOP | Gravity.START;
        back.setOnClickListener(v -> cancel());
        root.addView(back, backLp);

        // Bottom-center cancel pill, reusing the framework's localized string.
        TextView cancel = new TextView(this);
        cancel.setText(getString(android.R.string.cancel));
        cancel.setTextSize(15f);
        cancel.setTypeface(Typeface.DEFAULT_BOLD);
        cancel.setTextColor(Color.WHITE);
        cancel.setGravity(Gravity.CENTER);
        GradientDrawable pill = new GradientDrawable();
        pill.setColor(0x66000000);
        pill.setCornerRadius(24 * density);
        cancel.setBackground(pill);
        int padH = (int) (22 * density);
        int padV = (int) (12 * density);
        cancel.setPadding(padH, padV, padH, padV);
        FrameLayout.LayoutParams cancelLp = new FrameLayout.LayoutParams(
                ViewGroup.LayoutParams.WRAP_CONTENT, ViewGroup.LayoutParams.WRAP_CONTENT);
        cancelLp.bottomMargin = (int) (28 * density);
        cancelLp.gravity = Gravity.BOTTOM | Gravity.CENTER_HORIZONTAL;
        cancel.setOnClickListener(v -> cancel());
        root.addView(cancel, cancelLp);

        return root;
    }

    /** Bind the back camera: a Preview for the viewfinder plus a KEEP_ONLY_LATEST QR analyzer. */
    private void bindCamera() {
        barcodeScanner = BarcodeScanning.getClient(new BarcodeScannerOptions.Builder()
                .setBarcodeFormats(Barcode.FORMAT_QR_CODE)
                .build());
        cameraExecutor = Executors.newSingleThreadExecutor();
        ListenableFuture<ProcessCameraProvider> future = ProcessCameraProvider.getInstance(this);
        Executor main = ContextCompat.getMainExecutor(this);
        future.addListener(() -> {
            try {
                ProcessCameraProvider provider = future.get();
                cameraProvider = provider;
                Preview preview = new Preview.Builder().build();
                preview.setSurfaceProvider(previewView.getSurfaceProvider());
                ImageAnalysis analysis = new ImageAnalysis.Builder()
                        .setBackpressureStrategy(ImageAnalysis.STRATEGY_KEEP_ONLY_LATEST)
                        .build();
                analysis.setAnalyzer(cameraExecutor, this::analyze);
                provider.unbindAll();
                provider.bindToLifecycle(this, CameraSelector.DEFAULT_BACK_CAMERA, preview, analysis);
            } catch (Exception e) {
                Log.e(TAG, "bindCamera failed", e);
                cancel();
            }
        }, main);
    }

    /** Analyze one camera frame: first QR hit wins, everything else is dropped. */
    @OptIn(markerClass = ExperimentalGetImage.class)
    private void analyze(@NonNull ImageProxy proxy) {
        if (delivered || barcodeScanner == null || proxy.getImage() == null) {
            proxy.close();
            return;
        }
        InputImage input = InputImage.fromMediaImage(
                proxy.getImage(), proxy.getImageInfo().getRotationDegrees());
        // addOnCompleteListener covers success, failure AND cancel — the
        // single close point keeps the proxy from being closed twice.
        barcodeScanner.process(input)
                .addOnSuccessListener(barcodes -> {
                    if (barcodes == null) return;
                    for (Barcode barcode : barcodes) {
                        String raw = barcode.getRawValue();
                        if (raw != null && !raw.isEmpty()) {
                            deliver(raw);
                            break;
                        }
                    }
                })
                .addOnCompleteListener(task -> proxy.close());
    }

    /** Hand the decoded text to MainActivity (RESULT_OK) and close. */
    private void deliver(String text) {
        if (delivered) return;
        delivered = true;
        if (DEBUG) Log.d(TAG, "QR decoded");
        runOnUiThread(() -> {
            if (isFinishing() || isDestroyed()) return;
            setResult(RESULT_OK, new Intent().putExtra(EXTRA_TEXT, text));
            finish();
        });
    }

    /** Close with RESULT_CANCELED (back arrow, cancel pill, camera failure). */
    private void cancel() {
        setResult(RESULT_CANCELED);
        finish();
    }

    @Override
    protected void onDestroy() {
        super.onDestroy();
        if (cameraProvider != null) {
            try {
                cameraProvider.unbindAll();
            } catch (Exception e) {
                Log.e(TAG, "unbindAll failed", e);
            }
            cameraProvider = null;
        }
        if (barcodeScanner != null) {
            barcodeScanner.close();
            barcodeScanner = null;
        }
        if (cameraExecutor != null) {
            cameraExecutor.shutdown();
            cameraExecutor = null;
        }
    }
}
