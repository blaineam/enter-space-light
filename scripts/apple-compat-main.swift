import Foundation
let args = CommandLine.arguments
if args[1] == "write" {
    let d = ISO8601DateFormatter()
    let m = BackupManifest(
        runID: UUID(uuidString: "11111111-2222-4333-8444-555555555555")!,
        syncID: UUID(uuidString: "AAAAAAAA-BBBB-4CCC-8DDD-EEEEEEEEEEEE")!,
        syncName: "Home — Mac",
        startedAt: d.date(from: "2026-08-03T02:00:00Z")!, finishedAt: d.date(from: "2026-08-03T02:10:30Z")!,
        sourceFs: "/Users/me", destinationFs: "b2crypt:Backup/Home",
        backupDirFs: "b2crypt:.previous-versions/Backup-Home/2026-08-02_19-00-00",
        outcome: .completedWithErrors,
        stats: BackupManifest.Stats(filesTransferred: 3, filesDeleted: 1, filesChecked: 10, bytesTransferred: 4096, errors: 1, abortReason: nil),
        versionedEntries: [BackupManifest.Entry(path: "Documents/old.txt", size: 12, modTime: d.date(from: "2026-07-01T10:00:00Z")!, hash: "abc", hashKind: "md5")],
        liveEntries: [BackupManifest.Entry(path: "Documents/new.txt", size: 20, modTime: d.date(from: "2026-08-03T01:00:00Z")!),
                      BackupManifest.Entry(path: "Pictures/ünïcödé.jpg", size: 5, modTime: Date.distantPast, presence: .prunedByRetention)],
        treeCoverage: .partial)
    try! BackupManifestCodec.encode(m).write(to: URL(fileURLWithPath: args[2]))
} else {
    guard let m = BackupManifestCodec.decode(try! Data(contentsOf: URL(fileURLWithPath: args[2]))) else { print("DECODE FAILED"); exit(1) }
    print("ok", m.syncID, m.runID, m.syncName, m.outcome, m.treeCoverage, m.startedAt.timeIntervalSince1970, m.backupDirFs ?? "nil", m.stats.abortReason ?? "nil", m.liveEntries.count, m.versionedEntries.first?.hash ?? "nil", m.liveEntries.first?.presence.rawValue ?? "")
}
