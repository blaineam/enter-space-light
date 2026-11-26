package io.github.blaineam.enterspacelight

import android.app.AlarmManager
import android.app.Notification
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.os.Build
import android.os.Handler
import android.os.IBinder
import android.os.Looper
import android.os.PowerManager
import android.os.SystemClock
import android.util.Log
import io.github.blaineam.esl.mobile.Mobile
import org.json.JSONObject
import java.util.concurrent.Executors

/**
 * Keeps the engine alive: drives stay available to other apps and schedules
 * run. It holds the CPU awake only while something is being transferred.
 *
 * It is a "special use" service on purpose: Android 15 stops data-sync
 * services after six hours a day, which a mapped drive can't live with.
 */
class EngineService : Service() {
    private val main = Handler(Looper.getMainLooper())
    private val worker = Executors.newSingleThreadExecutor()
    private lateinit var wakeLock: PowerManager.WakeLock
    private var shown = ""
    private var stopping = false

    private val poll = object : Runnable {
        override fun run() {
            worker.execute { update() }
            main.postDelayed(this, 5_000)
        }
    }

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onCreate() {
        super.onCreate()
        wakeLock = getSystemService(PowerManager::class.java)
            .newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "EnterSpaceLight:transfers")
            .apply { setReferenceCounted(false) }
        instance = this
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        val n = notification("Starting…")
        try {
            if (Build.VERSION.SDK_INT >= 34) startForeground(ID, n, ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE)
            else startForeground(ID, n)
        } catch (e: Exception) {
            Log.w(Engine.TAG, "couldn't run in the foreground", e)
            stopSelf()
            return START_NOT_STICKY
        }
        if (intent?.action == ACTION_STOP) {
            stopping = true
            worker.execute {
                Mobile.stop()
                main.post { shutDown() }
            }
            return START_NOT_STICKY
        }
        stopping = false
        worker.execute { if (!Engine.tryStart(this)) main.post { shutDown() } }
        main.removeCallbacks(poll)
        main.postDelayed(poll, 1_000)
        scheduleTick(this)
        return START_STICKY
    }

    private fun update() {
        if (stopping || !Mobile.running()) return
        val a = JSONObject(Mobile.activityJSON())
        val drives = a.optInt("drives")
        val running = a.optInt("running")
        val uploads = a.optInt("uploads")
        val parts = mutableListOf<String>()
        parts += if (drives == 1) "1 drive connected" else "$drives drives connected"
        if (running > 0) parts += if (running == 1) "syncing" else "$running syncs running"
        if (uploads > 0) parts += if (uploads == 1) "uploading 1 file" else "uploading $uploads files"
        val text = parts.joinToString(" · ")
        val busy = running > 0 || uploads > 0 || a.optInt("openFiles") > 0
        // The timeout is a backstop: it is renewed while work continues.
        if (busy) wakeLock.acquire(10 * 60_000L) else if (wakeLock.isHeld) wakeLock.release()
        main.post {
            if (!stopping && text != shown) {
                shown = text
                getSystemService(android.app.NotificationManager::class.java).notify(ID, notification(text))
            }
        }
    }

    private fun notification(text: String): Notification {
        val open = PendingIntent.getActivity(this, 0, Intent(this, MainActivity::class.java), PendingIntent.FLAG_IMMUTABLE)
        val stop = PendingIntent.getService(
            this, 1, Intent(this, EngineService::class.java).setAction(ACTION_STOP), PendingIntent.FLAG_IMMUTABLE,
        )
        return Notification.Builder(this, App.CHANNEL_STATUS)
            .setSmallIcon(R.drawable.ic_stat)
            .setContentTitle("Enter Space Light")
            .setContentText(text)
            .setContentIntent(open)
            .setOngoing(true)
            .setOnlyAlertOnce(true)
            .addAction(Notification.Action.Builder(null, "Stop", stop).build())
            .build()
    }

    private fun shutDown() {
        stopping = true
        main.removeCallbacks(poll)
        cancelTick(this)
        if (wakeLock.isHeld) wakeLock.release()
        stopForeground(STOP_FOREGROUND_REMOVE)
        stopSelf()
    }

    override fun onDestroy() {
        main.removeCallbacks(poll)
        if (wakeLock.isHeld) wakeLock.release()
        worker.shutdown()
        instance = null
        super.onDestroy()
    }

    companion object {
        private const val ID = 1
        const val ACTION_STOP = "io.github.blaineam.enterspacelight.STOP"
        private const val TICK_EVERY = 15 * 60_000L

        @Volatile private var instance: EngineService? = null

        /** Starts the service if Android allows it from where we are. */
        fun start(context: Context) {
            try {
                context.startForegroundService(Intent(context, EngineService::class.java))
            } catch (e: Exception) {
                // Android refuses some background starts. The engine still
                // works while this process lives.
                Log.w(Engine.TAG, "couldn't start the background service", e)
            }
        }

        fun engineStopped(context: Context) {
            val s = instance ?: return
            s.main.post { s.shutDown() }
        }

        private fun tickIntent(c: Context) =
            PendingIntent.getBroadcast(c, 0, Intent(c, TickReceiver::class.java), PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)

        /** Timers stop while the phone dozes, so an alarm checks the schedules. */
        fun scheduleTick(c: Context) {
            c.getSystemService(AlarmManager::class.java).setAndAllowWhileIdle(
                AlarmManager.ELAPSED_REALTIME_WAKEUP, SystemClock.elapsedRealtime() + TICK_EVERY, tickIntent(c),
            )
        }

        private fun cancelTick(c: Context) = c.getSystemService(AlarmManager::class.java).cancel(tickIntent(c))
    }
}
