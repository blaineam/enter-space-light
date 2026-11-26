package io.github.blaineam.enterspacelight

import android.annotation.SuppressLint
import android.app.Activity
import android.content.Intent
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.view.Gravity
import android.view.View
import android.view.WindowInsets
import android.webkit.WebChromeClient
import android.webkit.WebResourceRequest
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.FrameLayout
import android.widget.TextView
import io.github.blaineam.esl.mobile.Mobile
import java.lang.ref.WeakReference
import java.util.concurrent.Executors

/** The engine's own settings screens, served on this phone, in a WebView. */
class MainActivity : Activity() {
    private lateinit var web: WebView
    private lateinit var message: TextView
    private val worker = Executors.newSingleThreadExecutor()

    /** host:port of the engine the page is signed in to. */
    private var loaded: String? = null

    @SuppressLint("SetJavaScriptEnabled")
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val root = FrameLayout(this)
        web = WebView(this).apply {
            settings.javaScriptEnabled = true
            settings.domStorageEnabled = true
            settings.allowFileAccess = false
            settings.allowContentAccess = false
            setBackgroundColor(getColor(R.color.window_bg))
            webChromeClient = WebChromeClient() // shows the page's confirm() dialogs
            webViewClient = object : WebViewClient() {
                // Only our own pages stay here; every other link goes to the browser.
                override fun shouldOverrideUrlLoading(view: WebView, request: WebResourceRequest): Boolean {
                    if (isEngine(request.url)) return false
                    if (request.url.scheme == "https" || request.url.scheme == "http") {
                        try {
                            startActivity(Intent(Intent.ACTION_VIEW, request.url))
                        } catch (e: Exception) {
                            android.util.Log.w(Engine.TAG, "no browser", e)
                        }
                    }
                    return true
                }
            }
        }
        message = TextView(this).apply {
            text = getString(R.string.app_name)
            gravity = Gravity.CENTER
            setPadding(48, 48, 48, 48)
        }
        root.addView(web)
        root.addView(message)
        // Android 15 draws apps edge to edge: keep clear of the bars and keyboard.
        if (Build.VERSION.SDK_INT >= 30) {
            root.setOnApplyWindowInsetsListener { v, insets ->
                val i = insets.getInsets(WindowInsets.Type.systemBars() or WindowInsets.Type.ime() or WindowInsets.Type.displayCutout())
                v.setPadding(i.left, i.top, i.right, i.bottom)
                WindowInsets.CONSUMED
            }
        }
        setContentView(root)
    }

    private fun isEngine(url: Uri) = url.scheme == "http" && url.host == "127.0.0.1" && url.authority == loaded

    override fun onResume() {
        super.onResume()
        AppHost.current = WeakReference(this)
        worker.execute {
            val problem = try {
                Engine.ensureStarted(this)
                null
            } catch (e: Exception) {
                android.util.Log.e(Engine.TAG, "engine failed to start", e)
                e.message ?: e.toString()
            }
            if (problem == null) Mobile.changed() // a permission may just have been granted
            val login = if (problem == null) Uri.parse(Mobile.loginURL()) else null
            runOnUiThread {
                if (isDestroyed) return@runOnUiThread
                if (login == null) {
                    message.text = "Enter Space Light couldn’t start.\n\n$problem"
                    message.visibility = View.VISIBLE
                    return@runOnUiThread
                }
                EngineService.start(this)
                if (loaded != login.authority) {
                    loaded = login.authority
                    web.loadUrl(login.toString())
                }
                message.visibility = View.GONE
            }
        }
    }

    override fun onRequestPermissionsResult(requestCode: Int, permissions: Array<out String>, grantResults: IntArray) {
        super.onRequestPermissionsResult(requestCode, permissions, grantResults)
        worker.execute { Mobile.changed() }
    }

    @Deprecated("Back closes an open dialog first")
    override fun onBackPressed() {
        web.evaluateJavascript(
            "(function(){var d=document.getElementById('dlg');if(d&&d.open){d.close();return true}return false})()",
        ) { closed -> if (closed != "true") moveTaskToBack(true) }
    }

    override fun onDestroy() {
        if (AppHost.current?.get() === this) AppHost.current = null
        web.destroy()
        worker.shutdown()
        super.onDestroy()
    }
}
