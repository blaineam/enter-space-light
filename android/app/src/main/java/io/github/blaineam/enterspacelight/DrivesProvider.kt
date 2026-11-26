package io.github.blaineam.enterspacelight

import android.database.Cursor
import android.database.MatrixCursor
import android.os.CancellationSignal
import android.os.Handler
import android.os.HandlerThread
import android.os.ParcelFileDescriptor
import android.os.ProxyFileDescriptorCallback
import android.os.storage.StorageManager
import android.provider.DocumentsContract
import android.provider.DocumentsContract.Document
import android.provider.DocumentsContract.Root
import android.provider.DocumentsProvider
import android.system.ErrnoException
import android.system.OsConstants
import android.util.Log
import android.webkit.MimeTypeMap
import io.github.blaineam.esl.mobile.Mobile
import org.json.JSONArray
import org.json.JSONObject
import java.io.FileNotFoundException

/**
 * Shows connected drives in the Files app and every app's file picker.
 * Android's answer to a mapped network drive: apps get real, seekable file
 * descriptors that read and write through rclone's cache.
 *
 * A document ID is "<drive id>:<path in the drive>"; drive IDs never
 * contain a colon.
 */
class DrivesProvider : DocumentsProvider() {

    override fun onCreate() = true

    private data class Id(val drive: String, val path: String) {
        override fun toString() = "$drive:$path"
        fun child(name: String) = Id(drive, if (path.isEmpty()) name else "$path/$name")
    }

    private fun parse(documentId: String): Id {
        val i = documentId.indexOf(':')
        if (i <= 0) throw FileNotFoundException("not one of our documents: $documentId")
        return Id(documentId.substring(0, i), documentId.substring(i + 1))
    }

    /** Runs an engine call, reporting failures the way the framework expects. */
    private fun <T> engine(what: String, call: () -> T): T = try {
        call()
    } catch (e: FileNotFoundException) {
        throw e
    } catch (e: Exception) {
        Log.w(Engine.TAG, "$what failed", e)
        throw FileNotFoundException("$what: ${e.message}")
    }

    /** Another app opening a file can be what starts us. */
    private fun ready(): Boolean {
        if (Mobile.running()) return true
        val c = context ?: return false
        if (!Engine.hasWork(c) || !Engine.tryStart(c)) return false
        EngineService.start(c)
        return true
    }

    private fun connected(drive: String) {
        if (!ready() || !Mobile.waitConnected(drive, 20_000)) {
            throw FileNotFoundException("This drive isn't connected yet. Open Enter Space Light to check it.")
        }
    }

    private fun drives(): List<JSONObject> {
        if (!ready()) return emptyList()
        val a = JSONArray(Mobile.drivesJSON())
        return (0 until a.length()).map { a.getJSONObject(it) }
    }

    private fun driveInfo(id: String): JSONObject =
        drives().firstOrNull { it.getString("id") == id } ?: throw FileNotFoundException("no such drive")

    override fun queryRoots(projection: Array<out String>?): Cursor {
        val cursor = MatrixCursor(projection ?: ROOT_COLUMNS)
        for (d in drives()) {
            val id = d.getString("id")
            val readOnly = d.getBoolean("readOnly")
            var flags = Root.FLAG_SUPPORTS_IS_CHILD
            if (!readOnly) flags = flags or Root.FLAG_SUPPORTS_CREATE
            var free: Long? = null
            if (d.getBoolean("connected")) {
                free = JSONObject(Mobile.spaceJSON(id)).getLong("free").takeIf { it >= 0 }
            }
            cursor.newRow()
                .add(Root.COLUMN_ROOT_ID, id)
                .add(Root.COLUMN_DOCUMENT_ID, Id(id, "").toString())
                .add(Root.COLUMN_TITLE, d.getString("name"))
                .add(Root.COLUMN_SUMMARY, if (d.getBoolean("connected")) "Enter Space Light" else "Connecting…")
                .add(Root.COLUMN_FLAGS, flags)
                .add(Root.COLUMN_ICON, R.mipmap.ic_launcher)
                .add(Root.COLUMN_AVAILABLE_BYTES, free)
        }
        cursor.setNotificationUri(context!!.contentResolver, DocumentsContract.buildRootsUri(AUTHORITY))
        return cursor
    }

    private fun mimeOf(name: String): String {
        val ext = name.substringAfterLast('.', "").lowercase()
        return MimeTypeMap.getSingleton().getMimeTypeFromExtension(ext) ?: "application/octet-stream"
    }

    private fun row(cursor: MatrixCursor, id: Id, name: String, dir: Boolean, size: Long, modified: Long, readOnly: Boolean) {
        var flags = 0
        if (!readOnly) {
            if (id.path.isNotEmpty()) {
                flags = flags or Document.FLAG_SUPPORTS_DELETE or Document.FLAG_SUPPORTS_REMOVE or
                    Document.FLAG_SUPPORTS_RENAME or Document.FLAG_SUPPORTS_MOVE
            }
            flags = flags or if (dir) Document.FLAG_DIR_SUPPORTS_CREATE else Document.FLAG_SUPPORTS_WRITE
        }
        cursor.newRow()
            .add(Document.COLUMN_DOCUMENT_ID, id.toString())
            .add(Document.COLUMN_DISPLAY_NAME, name)
            .add(Document.COLUMN_MIME_TYPE, if (dir) Document.MIME_TYPE_DIR else mimeOf(name))
            .add(Document.COLUMN_SIZE, if (dir) null else size)
            .add(Document.COLUMN_LAST_MODIFIED, modified.takeIf { it > 0 })
            .add(Document.COLUMN_FLAGS, flags)
    }

    private fun row(cursor: MatrixCursor, drive: String, e: JSONObject, readOnly: Boolean) = row(
        cursor, Id(drive, e.getString("path")), e.getString("name"), e.getBoolean("dir"),
        e.getLong("size"), e.getLong("modified"), readOnly,
    )

    override fun queryDocument(documentId: String, projection: Array<out String>?): Cursor {
        val id = parse(documentId)
        val cursor = MatrixCursor(projection ?: DOCUMENT_COLUMNS)
        ready()
        val info = driveInfo(id.drive)
        if (id.path.isEmpty()) {
            // The root is answered without touching the network.
            row(cursor, id, info.getString("name"), true, 0, 0, info.getBoolean("readOnly"))
        } else {
            connected(id.drive)
            val e = JSONObject(engine("looking up the file") { Mobile.stat(id.drive, id.path) })
            row(cursor, id.drive, e, info.getBoolean("readOnly"))
        }
        return cursor
    }

    override fun queryChildDocuments(parentDocumentId: String, projection: Array<out String>?, sortOrder: String?): Cursor {
        val parent = parse(parentDocumentId)
        connected(parent.drive)
        val readOnly = driveInfo(parent.drive).getBoolean("readOnly")
        val cursor = MatrixCursor(projection ?: DOCUMENT_COLUMNS)
        val list = JSONArray(engine("listing the folder") { Mobile.list(parent.drive, parent.path) })
        for (i in 0 until list.length()) row(cursor, parent.drive, list.getJSONObject(i), readOnly)
        cursor.setNotificationUri(context!!.contentResolver, childrenUri(parent))
        return cursor
    }

    private fun childrenUri(parent: Id) = DocumentsContract.buildChildDocumentsUri(AUTHORITY, parent.toString())

    private fun changed(id: Id) {
        val parent = Id(id.drive, id.path.substringBeforeLast('/', ""))
        context?.contentResolver?.notifyChange(childrenUri(parent), null)
    }

    private fun writable(drive: String) {
        connected(drive)
        if (driveInfo(drive).getBoolean("readOnly")) throw UnsupportedOperationException("This drive is read-only")
    }

    override fun createDocument(parentDocumentId: String, mimeType: String, displayName: String): String {
        val parent = parse(parentDocumentId)
        writable(parent.drive)
        val path = engine("creating $displayName") {
            Mobile.create(parent.drive, parent.path, displayName, mimeType == Document.MIME_TYPE_DIR)
        }
        val made = Id(parent.drive, path)
        changed(made)
        return made.toString()
    }

    override fun deleteDocument(documentId: String) {
        val id = parse(documentId)
        writable(id.drive)
        engine("deleting") { Mobile.delete(id.drive, id.path) }
        revokeDocumentPermission(documentId)
        changed(id)
    }

    override fun removeDocument(documentId: String, parentDocumentId: String) = deleteDocument(documentId)

    override fun renameDocument(documentId: String, displayName: String): String {
        val id = parse(documentId)
        writable(id.drive)
        val renamed = Id(id.drive, engine("renaming") { Mobile.rename(id.drive, id.path, displayName) })
        changed(id)
        return renamed.toString()
    }

    override fun moveDocument(sourceDocumentId: String, sourceParentDocumentId: String, targetParentDocumentId: String): String {
        val id = parse(sourceDocumentId)
        val target = parse(targetParentDocumentId)
        if (id.drive != target.drive) throw UnsupportedOperationException("Copy between drives instead of moving")
        writable(id.drive)
        val moved = Id(id.drive, engine("moving") { Mobile.move(id.drive, id.path, target.path) })
        changed(id)
        changed(moved)
        return moved.toString()
    }

    override fun isChildDocument(parentDocumentId: String, documentId: String): Boolean {
        val parent = parse(parentDocumentId)
        val child = parse(documentId)
        if (parent.drive != child.drive || child.path.split('/').contains("..")) return false
        return parent.path.isEmpty() || child.path.startsWith(parent.path + "/") || child.path == parent.path
    }

    override fun openDocument(documentId: String, mode: String, signal: CancellationSignal?): ParcelFileDescriptor {
        val id = parse(documentId)
        val writes = mode.contains('w')
        if (writes) writable(id.drive) else connected(id.drive)
        val handle = engine("opening the file") { Mobile.open(id.drive, id.path, mode) }
        // Each open file has its own thread, so one slow download can't
        // stall every other file.
        val thread = HandlerThread("esl-file-$handle").apply { start() }
        val access = when {
            !writes -> ParcelFileDescriptor.MODE_READ_ONLY
            mode.startsWith("rw") -> ParcelFileDescriptor.MODE_READ_WRITE
            mode == "wa" -> ParcelFileDescriptor.MODE_WRITE_ONLY or ParcelFileDescriptor.MODE_APPEND
            else -> ParcelFileDescriptor.MODE_WRITE_ONLY
        }
        return try {
            // Where "append" starts. Android passes the flag on but doesn't
            // apply it: the app's writes still arrive counted from zero.
            val appendAt = if (mode == "wa") Mobile.size(handle) else 0L
            context!!.getSystemService(StorageManager::class.java)
                .openProxyFileDescriptor(access, FileCallback(id, handle, writes, appendAt, thread), Handler(thread.looper))
        } catch (e: Exception) {
            Mobile.closeFile(handle)
            thread.quitSafely()
            throw FileNotFoundException("opening the file: ${e.message}")
        }
    }

    private inner class FileCallback(
        private val id: Id,
        private val handle: Long,
        private val writes: Boolean,
        private val appendAt: Long,
        private val thread: HandlerThread,
    ) : ProxyFileDescriptorCallback() {
        /** Added to every write; decided by the first one. */
        private var shift: Long? = null

        private fun <T> io(what: String, call: () -> T): T = try {
            call()
        } catch (e: Exception) {
            Log.w(Engine.TAG, "$what failed", e)
            throw ErrnoException(what, OsConstants.EIO)
        }

        override fun onGetSize(): Long = io("size") { Mobile.size(handle) }

        override fun onRead(offset: Long, size: Int, data: ByteArray): Int =
            io("read") { Mobile.readAt(handle, offset, data, size.toLong()).toInt() }

        override fun onWrite(offset: Long, size: Int, data: ByteArray): Int {
            // A first write before the end of the file means append wasn't
            // applied. If a system ever does apply it, nothing is moved.
            val move = shift ?: (if (offset < appendAt) appendAt else 0L).also { shift = it }
            return io("write") { Mobile.writeAt(handle, offset + move, data, size.toLong()).toInt() }
        }

        override fun onFsync() {
            io("fsync") { Mobile.sync(handle) }
        }

        override fun onRelease() {
            try {
                Mobile.closeFile(handle)
            } catch (e: Exception) {
                Log.e(Engine.TAG, "closing a file failed", e)
            }
            if (writes) changed(id)
            thread.quitSafely()
        }
    }

    companion object {
        const val AUTHORITY = "io.github.blaineam.enterspacelight.drives"
        private val ROOT_COLUMNS = arrayOf(
            Root.COLUMN_ROOT_ID, Root.COLUMN_DOCUMENT_ID, Root.COLUMN_TITLE, Root.COLUMN_SUMMARY,
            Root.COLUMN_FLAGS, Root.COLUMN_ICON, Root.COLUMN_AVAILABLE_BYTES,
        )
        private val DOCUMENT_COLUMNS = arrayOf(
            Document.COLUMN_DOCUMENT_ID, Document.COLUMN_DISPLAY_NAME, Document.COLUMN_MIME_TYPE,
            Document.COLUMN_SIZE, Document.COLUMN_LAST_MODIFIED, Document.COLUMN_FLAGS,
        )
    }
}
