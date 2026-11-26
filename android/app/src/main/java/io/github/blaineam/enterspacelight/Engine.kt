package io.github.blaineam.enterspacelight

import android.content.Context
import android.os.Build
import android.os.Environment
import android.util.Log
import io.github.blaineam.esl.mobile.Mobile
import java.io.File
import java.util.TimeZone

/** Starts the Go engine once per process, from whichever entry point comes first. */
object Engine {
    const val TAG = "EnterSpaceLight"

    private var monitors: Monitors? = null

    /** Blocks while rclone loads; don't call it on the main thread. */
    @Synchronized
    fun ensureStarted(context: Context) {
        if (Mobile.running()) return
        val app = context.applicationContext
        Mobile.setHost(AppHost(app))
        // Network, charger and drives are reported first, so the very first
        // scheduler pass already knows whether it may use the network.
        val m = monitors ?: Monitors(app).also { monitors = it }
        m.report()
        @Suppress("DEPRECATION")
        val storage = Environment.getExternalStorageDirectory().absolutePath
        Mobile.start(
            app.filesDir.absolutePath,
            app.cacheDir.absolutePath,
            storage,
            Build.MODEL ?: "Android",
            TimeZone.getDefault().id,
        )
        m.watch()
        Log.i(TAG, "engine started")
    }

    /** Starts the engine, logging instead of throwing; false if it failed. */
    fun tryStart(context: Context): Boolean = try {
        ensureStarted(context)
        true
    } catch (e: Exception) {
        Log.e(TAG, "engine failed to start", e)
        false
    }

    /** Whether the user has set up any drive or sync: nothing else is worth waking for. */
    fun hasWork(context: Context): Boolean {
        val f = File(context.filesDir, "config/settings.json")
        if (!f.isFile) return false
        return try {
            val doc = org.json.JSONObject(f.readText())
            (doc.optJSONArray("mounts")?.length() ?: 0) > 0 || (doc.optJSONArray("jobs")?.length() ?: 0) > 0
        } catch (e: Exception) {
            Log.w(TAG, "reading settings", e)
            false
        }
    }
}
