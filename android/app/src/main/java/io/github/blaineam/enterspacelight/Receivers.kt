package io.github.blaineam.enterspacelight

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.os.PowerManager
import io.github.blaineam.esl.mobile.Mobile
import kotlin.concurrent.thread

/** Starts with the phone (or after an update) when there is something to do. */
class BootReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        if (intent.action != Intent.ACTION_BOOT_COMPLETED && intent.action != Intent.ACTION_MY_PACKAGE_REPLACED) return
        if (Prefs.startOnBoot(context) && Engine.hasWork(context)) EngineService.start(context)
    }
}

/** The periodic alarm: wake up long enough to start whatever is due. */
class TickReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        EngineService.scheduleTick(context)
        val pending = goAsync()
        val wake = context.getSystemService(PowerManager::class.java)
            .newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "EnterSpaceLight:tick")
        wake.acquire(60_000)
        thread {
            try {
                if (!Mobile.running()) {
                    if (!Engine.hasWork(context)) return@thread
                    EngineService.start(context)
                    Engine.tryStart(context)
                }
                Mobile.tick()
                // Give a sync that just started time to take its own wake lock.
                Thread.sleep(7_000)
            } finally {
                if (wake.isHeld) wake.release()
                pending.finish()
            }
        }
    }
}
