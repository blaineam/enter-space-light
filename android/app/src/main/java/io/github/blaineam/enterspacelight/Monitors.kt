package io.github.blaineam.enterspacelight

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import android.os.BatteryManager
import android.os.Build
import android.os.Environment
import android.os.Handler
import android.os.HandlerThread
import android.os.storage.StorageManager
import android.os.storage.StorageVolume
import android.util.Log
import io.github.blaineam.esl.mobile.Mobile
import org.json.JSONArray
import org.json.JSONObject
import java.io.File

/** Tells the engine about the network, the charger and storage drives. */
class Monitors(private val context: Context) {
    private val thread = HandlerThread("esl-monitors").apply { start() }
    private val handler = Handler(thread.looper)
    private val connectivity = context.getSystemService(ConnectivityManager::class.java)
    private val storage = context.getSystemService(StorageManager::class.java)
    private var watching = false

    @Volatile private var capabilities: NetworkCapabilities? =
        connectivity.activeNetwork?.let { connectivity.getNetworkCapabilities(it) }
    private var lastPower: Triple<Boolean, Boolean, Boolean>? = null

    fun report() {
        reportPower()
        reportVolumes()
    }

    @Synchronized
    private fun reportPower() {
        val caps = capabilities
        // Not "validated": a home network with a NAS and no internet is still a network.
        val online = caps?.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET) == true
        val unmetered = online && caps?.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_METERED) == true
        val charging = context.getSystemService(BatteryManager::class.java).isCharging
        val now = Triple(online, unmetered, charging)
        // Capability callbacks fire for signal strength too.
        if (now == lastPower && Mobile.running()) return
        lastPower = now
        Mobile.setPower(online, unmetered, charging)
    }

    private fun reportVolumes() {
        val list = JSONArray()
        for (v in storage.storageVolumes) {
            if (v.state != Environment.MEDIA_MOUNTED && v.state != Environment.MEDIA_MOUNTED_READ_ONLY) continue
            val root = rootOf(v) ?: continue
            list.put(
                JSONObject()
                    .put("id", v.uuid ?: if (v.isPrimary) "primary" else root)
                    .put("label", v.getDescription(context))
                    .put("root", root)
                    .put("removable", v.isRemovable && !v.isPrimary)
                    .put("system", v.isPrimary),
            )
        }
        try {
            Mobile.setVolumes(list.toString())
        } catch (e: Exception) {
            Log.w(Engine.TAG, "reporting drives", e)
        }
    }

    private fun rootOf(v: StorageVolume): String? {
        if (Build.VERSION.SDK_INT >= 30) return v.directory?.absolutePath
        if (v.isPrimary) {
            @Suppress("DEPRECATION")
            return Environment.getExternalStorageDirectory().absolutePath
        }
        return v.uuid?.let { File("/storage/$it") }?.takeIf { it.isDirectory }?.absolutePath
    }

    @Synchronized
    fun watch() {
        if (watching) return
        watching = true
        connectivity.registerDefaultNetworkCallback(
            object : ConnectivityManager.NetworkCallback() {
                override fun onCapabilitiesChanged(network: Network, networkCapabilities: NetworkCapabilities) {
                    capabilities = networkCapabilities
                    reportPower()
                }

                override fun onLost(network: Network) {
                    capabilities = null
                    reportPower()
                }
            },
            handler,
        )
        val receiver = object : BroadcastReceiver() {
            override fun onReceive(c: Context, intent: Intent) {
                when (intent.action) {
                    // The charging flag settles a moment after the broadcast.
                    Intent.ACTION_POWER_CONNECTED, Intent.ACTION_POWER_DISCONNECTED,
                    BatteryManager.ACTION_CHARGING, BatteryManager.ACTION_DISCHARGING,
                    -> handler.postDelayed({ reportPower() }, 1500)
                    else -> handler.postDelayed({ reportVolumes() }, 1000)
                }
            }
        }
        val power = IntentFilter().apply {
            addAction(Intent.ACTION_POWER_CONNECTED)
            addAction(Intent.ACTION_POWER_DISCONNECTED)
            addAction(BatteryManager.ACTION_CHARGING)
            addAction(BatteryManager.ACTION_DISCHARGING)
        }
        val media = IntentFilter().apply {
            addAction(Intent.ACTION_MEDIA_MOUNTED)
            addAction(Intent.ACTION_MEDIA_UNMOUNTED)
            addAction(Intent.ACTION_MEDIA_REMOVED)
            addAction(Intent.ACTION_MEDIA_EJECT)
            addDataScheme("file")
        }
        val zone = IntentFilter(Intent.ACTION_TIMEZONE_CHANGED)
        val zoneReceiver = object : BroadcastReceiver() {
            override fun onReceive(c: Context, intent: Intent) {
                // The broadcast carries the new ID; TimeZone's cache may lag.
                Mobile.setTimezone(intent.getStringExtra("time-zone") ?: java.util.TimeZone.getDefault().id)
            }
        }
        if (Build.VERSION.SDK_INT >= 33) context.registerReceiver(zoneReceiver, zone, null, handler, Context.RECEIVER_NOT_EXPORTED)
        else context.registerReceiver(zoneReceiver, zone, null, handler)
        for (f in listOf(power, media)) {
            if (Build.VERSION.SDK_INT >= 33) context.registerReceiver(receiver, f, null, handler, Context.RECEIVER_NOT_EXPORTED)
            else context.registerReceiver(receiver, f, null, handler)
        }
        if (Build.VERSION.SDK_INT >= 30) {
            storage.registerStorageVolumeCallback(
                { handler.post(it) },
                object : StorageManager.StorageVolumeCallback() {
                    override fun onStateChanged(volume: StorageVolume) {
                        handler.postDelayed({ reportVolumes() }, 1000)
                    }
                },
            )
        }
    }
}
