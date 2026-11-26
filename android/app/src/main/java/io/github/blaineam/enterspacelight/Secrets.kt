package io.github.blaineam.enterspacelight

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Base64
import android.util.Log
import java.security.KeyStore
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

/** The saved rclone.conf password, encrypted with a key that never leaves the Android Keystore. */
object Secrets {
    private const val ALIAS = "esl-rclone-conf"
    private const val FILE = "secrets"
    private const val NAME = "rcloneConf"

    private fun key(): SecretKey {
        val store = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        (store.getKey(ALIAS, null) as? SecretKey)?.let { return it }
        return KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore").apply {
            init(
                KeyGenParameterSpec.Builder(ALIAS, KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
                    .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                    .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                    .build(),
            )
        }.generateKey()
    }

    fun save(c: Context, value: String): Boolean = try {
        val prefs = c.getSharedPreferences(FILE, Context.MODE_PRIVATE)
        if (value.isEmpty()) {
            prefs.edit().remove(NAME).commit()
        } else {
            val cipher = Cipher.getInstance("AES/GCM/NoPadding").apply { init(Cipher.ENCRYPT_MODE, key()) }
            val sealed = cipher.iv + cipher.doFinal(value.toByteArray())
            prefs.edit().putString(NAME, Base64.encodeToString(sealed, Base64.NO_WRAP)).commit()
        }
    } catch (e: Exception) {
        Log.w(Engine.TAG, "saving the password", e)
        false
    }

    fun load(c: Context): String = try {
        val stored = c.getSharedPreferences(FILE, Context.MODE_PRIVATE).getString(NAME, null)
        if (stored == null) {
            ""
        } else {
            val sealed = Base64.decode(stored, Base64.NO_WRAP)
            val cipher = Cipher.getInstance("AES/GCM/NoPadding")
                .apply { init(Cipher.DECRYPT_MODE, key(), GCMParameterSpec(128, sealed, 0, 12)) }
            String(cipher.doFinal(sealed, 12, sealed.size - 12))
        }
    } catch (e: Exception) {
        Log.w(Engine.TAG, "reading the password", e)
        ""
    }
}
