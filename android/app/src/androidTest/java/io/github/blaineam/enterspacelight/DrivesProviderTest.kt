package io.github.blaineam.enterspacelight

import android.net.Uri
import android.provider.DocumentsContract
import android.provider.DocumentsContract.Document
import android.provider.DocumentsContract.Root
import android.system.Os
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import io.github.blaineam.esl.mobile.Mobile
import org.json.JSONObject
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Assume.assumeTrue
import org.junit.Test
import org.junit.runner.RunWith
import java.io.File
import java.io.FileInputStream
import java.io.FileOutputStream
import java.net.HttpURLConnection
import java.net.URL
import java.nio.ByteBuffer
import java.security.MessageDigest
import kotlin.random.Random

/**
 * Drives the DocumentsProvider the way the Files app and other apps do, over
 * a real network remote that scripts/e2e-android.py serves. That script then
 * checks on the server that every change was uploaded.
 *
 *   am instrument -w -e davURL http://10.0.2.2:18765 …
 */
@RunWith(AndroidJUnit4::class)
class DrivesProviderTest {
    private val context = InstrumentationRegistry.getInstrumentation().targetContext
    private val resolver = context.contentResolver
    private val authority = DrivesProvider.AUTHORITY

    private fun api(method: String, path: String, body: JSONObject? = null): String {
        val hs = JSONObject(File(context.filesDir, "state/api.json").readText())
        val c = URL(hs.getString("url") + path).openConnection() as HttpURLConnection
        c.requestMethod = method
        c.setRequestProperty("Authorization", "Bearer " + hs.getString("token"))
        c.setRequestProperty("X-ESL", "1")
        c.readTimeout = 60_000
        if (body != null) {
            c.doOutput = true
            c.outputStream.use { it.write(body.toString().toByteArray()) }
        }
        val ok = c.responseCode < 300
        val text = (if (ok) c.inputStream else c.errorStream).use { it.readBytes().decodeToString() }
        check(ok) { "$method $path: ${c.responseCode} $text" }
        return text
    }

    private fun addDrive(name: String, remote: String, readOnly: Boolean): String {
        val id = JSONObject(
            api(
                "POST", "/api/mounts",
                JSONObject().put("name", name).put("remote", remote).put("mountPoint", "")
                    .put("autoMount", true).put("readOnly", readOnly),
            ),
        ).getString("id")
        assertTrue("$name didn't connect", Mobile.waitConnected(id, 30_000))
        return id
    }

    private fun doc(id: String) = DocumentsContract.buildDocumentUri(authority, id)

    private data class Item(val id: String, val name: String, val mime: String, val size: Long, val flags: Int)

    private fun query(uri: Uri): List<Item> {
        val out = mutableListOf<Item>()
        resolver.query(uri, null, null, null, null)!!.use { c ->
            fun col(n: String) = c.getColumnIndexOrThrow(n)
            while (c.moveToNext()) {
                out += Item(
                    c.getString(col(Document.COLUMN_DOCUMENT_ID)), c.getString(col(Document.COLUMN_DISPLAY_NAME)),
                    c.getString(col(Document.COLUMN_MIME_TYPE)), c.getLong(col(Document.COLUMN_SIZE)),
                    c.getInt(col(Document.COLUMN_FLAGS)),
                )
            }
        }
        return out
    }

    private fun children(id: String) = query(DocumentsContract.buildChildDocumentsUri(authority, id))

    private fun read(id: String): ByteArray =
        resolver.openFileDescriptor(doc(id), "r")!!.use { FileInputStream(it.fileDescriptor).readBytes() }

    private fun write(id: String, mode: String, bytes: ByteArray) =
        resolver.openFileDescriptor(doc(id), mode)!!.use { pfd ->
            FileOutputStream(pfd.fileDescriptor).use { it.write(bytes) }
        }

    private fun create(parent: String, mime: String, name: String): String =
        DocumentsContract.getDocumentId(DocumentsContract.createDocument(resolver, doc(parent), mime, name)!!)

    private fun fails(block: () -> Any?): Boolean = try {
        block() == null
    } catch (e: Exception) {
        true
    }

    private fun sha(b: ByteArray) = MessageDigest.getInstance("SHA-256").digest(b).joinToString("") { "%02x".format(it) }

    @Test
    fun filesAppOperationsReachTheRemote() {
        val davURL = InstrumentationRegistry.getArguments().getString("davURL")
        assumeTrue("needs -e davURL (run scripts/e2e-android.py)", davURL != null)
        Engine.ensureStarted(context)
        api(
            "POST", "/api/remotes/config",
            JSONObject().put("name", "e2edav").put("type", "webdav")
                .put("parameters", JSONObject().put("url", davURL).put("vendor", "rclone")),
        )
        val drive = addDrive("E2E Drive", "e2edav:", false)

        // The drive is a root in every file picker.
        var found = false
        resolver.query(DocumentsContract.buildRootsUri(authority), null, null, null, null)!!.use { c ->
            while (c.moveToNext()) {
                if (c.getString(c.getColumnIndexOrThrow(Root.COLUMN_ROOT_ID)) != drive) continue
                found = true
                assertEquals("E2E Drive", c.getString(c.getColumnIndexOrThrow(Root.COLUMN_TITLE)))
                assertTrue(c.getInt(c.getColumnIndexOrThrow(Root.COLUMN_FLAGS)) and Root.FLAG_SUPPORTS_CREATE != 0)
            }
        }
        assertTrue("drive missing from roots", found)
        val root = "$drive:"
        assertEquals("E2E Drive", query(doc(root)).single().name)

        val docs = children(root).single { it.name == "docs" }
        assertEquals(Document.MIME_TYPE_DIR, docs.mime)
        val hello = children(docs.id).single { it.name == "hello.txt" }
        assertEquals("text/plain", hello.mime)
        assertEquals("hello from the server\n", read(hello.id).decodeToString())
        assertTrue(DocumentsContract.isChildDocument(resolver, doc(root), doc(hello.id)))

        // A real file descriptor: apps can seek.
        resolver.openFileDescriptor(doc(hello.id), "r")!!.use { pfd ->
            assertEquals(22L, pfd.statSize)
            val buf = ByteBuffer.allocate(4)
            FileInputStream(pfd.fileDescriptor).channel.read(buf, 6)
            assertEquals("from", buf.array().decodeToString())
        }

        val folder = create(root, Document.MIME_TYPE_DIR, "e2e")
        val note = create(folder, "text/plain", "note.txt")
        write(note, "w", "a long first version of the note".toByteArray())
        assertEquals("a long first version of the note", read(note).decodeToString())

        // Change a few bytes in the middle, as an editor or database does.
        resolver.openFileDescriptor(doc(note), "rw")!!.use { pfd ->
            Os.pwrite(pfd.fileDescriptor, "LONG".toByteArray(), 0, 4, 2)
        }
        assertEquals("a LONG first version of the note", read(note).decodeToString())

        // Saving something shorter leaves no tail behind.
        write(note, "w", "short".toByteArray())
        assertEquals("short", read(note).decodeToString())

        // Appending adds to the end, including over several writes that
        // together are longer than what was already there.
        resolver.openFileDescriptor(doc(note), "wa")!!.use { pfd ->
            FileOutputStream(pfd.fileDescriptor).use { out ->
                out.write(" and".toByteArray())
                out.write(" appended".toByteArray())
            }
        }
        assertEquals("short and appended", read(note).decodeToString())

        // A file far bigger than any buffer, written in odd-sized pieces.
        val big = Random(7).nextBytes(12 * 1024 * 1024 + 123)
        val bigDoc = create(folder, "application/octet-stream", "big.bin")
        resolver.openFileDescriptor(doc(bigDoc), "w")!!.use { pfd ->
            FileOutputStream(pfd.fileDescriptor).use { out ->
                var at = 0
                while (at < big.size) {
                    val n = minOf(61_003, big.size - at)
                    out.write(big, at, n)
                    at += n
                }
            }
        }
        assertArrayEquals(big, read(bigDoc))
        write(create(folder, "text/plain", "big.sha256"), "w", sha(big).toByteArray())

        // Rename and move never replace another file.
        val other = create(folder, "text/plain", "other.txt")
        assertTrue(fails { DocumentsContract.renameDocument(resolver, doc(other), "note.txt") })
        val renamed = DocumentsContract.getDocumentId(DocumentsContract.renameDocument(resolver, doc(note), "renamed.txt")!!)
        val sub = create(folder, Document.MIME_TYPE_DIR, "sub")
        val moved = DocumentsContract.getDocumentId(DocumentsContract.moveDocument(resolver, doc(renamed), doc(folder), doc(sub))!!)
        assertEquals("short and appended", read(moved).decodeToString())
        assertTrue(DocumentsContract.deleteDocument(resolver, doc(other)))
        assertEquals(setOf("sub", "big.bin", "big.sha256"), children(folder).map { it.name }.toSet())

        // A read-only drive refuses every change.
        val locked = addDrive("E2E Read Only", "e2edav:docs", true)
        val lockedHello = children("$locked:").single { it.name == "hello.txt" }
        assertEquals(0, lockedHello.flags and (Document.FLAG_SUPPORTS_WRITE or Document.FLAG_SUPPORTS_DELETE))
        assertTrue(fails { create("$locked:", "text/plain", "nope.txt") })
        assertTrue(fails { resolver.openFileDescriptor(doc(lockedHello.id), "w") })
        assertTrue(fails { DocumentsContract.deleteDocument(resolver, doc(lockedHello.id)) })
        assertEquals("hello from the server\n", read(lockedHello.id).decodeToString())

        // Nothing is left to upload before the host checks the server.
        val deadline = System.currentTimeMillis() + 90_000
        while (JSONObject(Mobile.activityJSON()).getInt("uploads") > 0) {
            check(System.currentTimeMillis() < deadline) { "uploads never finished" }
            Thread.sleep(250)
        }
        for (d in listOf(drive, locked)) api("DELETE", "/api/mounts/$d")
    }
}
