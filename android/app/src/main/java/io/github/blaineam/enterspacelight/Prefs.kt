package io.github.blaineam.enterspacelight

import android.content.Context

object Prefs {
    private fun prefs(c: Context) = c.getSharedPreferences("app", Context.MODE_PRIVATE)

    /** On unless turned off, as on the desktop. */
    fun startOnBoot(c: Context) = prefs(c).getBoolean("startOnBoot", true)
    fun setStartOnBoot(c: Context, on: Boolean) = prefs(c).edit().putBoolean("startOnBoot", on).apply()

    fun askedNotifications(c: Context) = prefs(c).getBoolean("askedNotifications", false)
    fun setAskedNotifications(c: Context) = prefs(c).edit().putBoolean("askedNotifications", true).apply()
}
