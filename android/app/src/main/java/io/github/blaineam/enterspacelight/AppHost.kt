package io.github.blaineam.enterspacelight

import android.Manifest
import android.annotation.SuppressLint
import android.app.Activity
import android.app.Notification
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.os.Environment
import android.os.PowerManager
import android.provider.DocumentsContract
import android.provider.Settings
import android.util.Log
import io.github.blaineam.esl.mobile.Host
import org.json.JSONArray
import org.json.JSONObject
import java.lang.ref.WeakReference

/** Everything the Go engine asks of Android. Calls arrive on Go's threads. */
class AppHost(private val context: Context) : Host {

    override fun openURL(url: String): Boolean {
        val uri = Uri.parse(url)
        if (uri.scheme != "http" && uri.scheme != "https") return false
        return start(Intent(Intent.ACTION_VIEW, uri))
    }

    override fun openDrive(id: String): Boolean {
        val root = DocumentsContract.buildRootUri(DrivesProvider.AUTHORITY, id)
        return start(Intent(Intent.ACTION_VIEW).setDataAndType(root, DocumentsContract.Root.MIME_TYPE_ITEM))
    }

    private fun start(intent: Intent): Boolean = try {
        val activity = current?.get()
        if (activity != null) activity.startActivity(intent)
        else context.startActivity(intent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
        true
    } catch (e: Exception) {
        Log.w(Engine.TAG, "couldn't open $intent", e)
        false
    }

    override fun notify(title: String, message: String) {
        val open = PendingIntent.getActivity(
            context, 0, Intent(context, MainActivity::class.java), PendingIntent.FLAG_IMMUTABLE,
        )
        val n = Notification.Builder(context, App.CHANNEL_ALERTS)
            .setSmallIcon(R.drawable.ic_stat)
            .setContentTitle(title)
            .setContentText(message)
            .setStyle(Notification.BigTextStyle().bigText(message))
            .setContentIntent(open)
            .setAutoCancel(true)
            .build()
        // Without the notification permission this does nothing.
        context.getSystemService(NotificationManager::class.java).notify(title.hashCode(), n)
    }

    override fun startsOnBoot(): Boolean = Prefs.startOnBoot(context)

    override fun setStartOnBoot(on: Boolean): Boolean {
        Prefs.setStartOnBoot(context, on)
        return true
    }

    override fun loadSecret(): String = Secrets.load(context)

    override fun saveSecret(value: String): Boolean = Secrets.save(context, value)

    override fun permissionsJSON(): String = JSONArray().apply {
        put(permission("files", "Access to files on this phone", "Needed to back up the phone’s files or USB drives, or to sync into a folder on the phone. Cloud drives work without it.", hasFiles(), true))
        put(permission("notifications", "Notifications", "So you hear about a backup that failed or was stopped to protect your files.", hasNotifications(), false))
        put(permission("battery", "Run without battery restrictions", "Lets scheduled backups run on time while the phone is idle.", ignoresBatteryLimits(), false))
    }.toString()

    private fun permission(id: String, title: String, why: String, granted: Boolean, required: Boolean) =
        JSONObject().put("id", id).put("title", title).put("why", why).put("granted", granted).put("required", required)

    private fun granted(name: String) = context.checkSelfPermission(name) == PackageManager.PERMISSION_GRANTED

    private fun hasFiles() =
        if (Build.VERSION.SDK_INT >= 30) Environment.isExternalStorageManager()
        else granted(Manifest.permission.WRITE_EXTERNAL_STORAGE)

    private fun hasNotifications() =
        context.getSystemService(NotificationManager::class.java).areNotificationsEnabled()

    private fun ignoresBatteryLimits() =
        context.getSystemService(PowerManager::class.java).isIgnoringBatteryOptimizations(context.packageName)

    @SuppressLint("BatteryLife") // outside Play; the user asks for this from Settings
    override fun requestPermission(id: String) {
        val pkg = Uri.parse("package:${context.packageName}")
        val activity = current?.get()
        when (id) {
            "files" ->
                if (Build.VERSION.SDK_INT >= 30) {
                    if (!start(Intent(Settings.ACTION_MANAGE_APP_ALL_FILES_ACCESS_PERMISSION, pkg))) {
                        start(Intent(Settings.ACTION_MANAGE_ALL_FILES_ACCESS_PERMISSION))
                    }
                } else {
                    activity?.runOnUiThread {
                        activity.requestPermissions(arrayOf(Manifest.permission.WRITE_EXTERNAL_STORAGE), 1)
                    }
                }
            "notifications" ->
                if (Build.VERSION.SDK_INT >= 33 && activity != null && !Prefs.askedNotifications(context)) {
                    Prefs.setAskedNotifications(context)
                    activity.runOnUiThread {
                        activity.requestPermissions(arrayOf(Manifest.permission.POST_NOTIFICATIONS), 2)
                    }
                } else {
                    // Android only shows its prompt once; after that it's a settings screen.
                    start(Intent(Settings.ACTION_APP_NOTIFICATION_SETTINGS).putExtra(Settings.EXTRA_APP_PACKAGE, context.packageName))
                }
            "battery" -> start(Intent(Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS, pkg))
        }
    }

    override fun drivesChanged() {
        context.contentResolver.notifyChange(DocumentsContract.buildRootsUri(DrivesProvider.AUTHORITY), null)
    }

    override fun stopped() {
        EngineService.engineStopped(context)
        drivesChanged()
    }

    companion object {
        /** The visible screen, if any: permission prompts need one. */
        @Volatile var current: WeakReference<Activity>? = null
    }
}
