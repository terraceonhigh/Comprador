import Foundation
import UserNotifications

/// Drives Mode A resumable uploads invisibly to the user.
///
/// Polls the bridge's `/_comprador/sessions` endpoint every few seconds.
/// When the bridge reports a stranded upload (Apple WebDAVFS truncated a
/// chunked PUT at its writeseq cap), this class:
///
///   1. Looks up the source file on the user's Mac via `NSMetadataQuery`,
///      filtering on `(filename, exact-byte-size)` for high specificity.
///   2. Opens the source file at the bridge's `received_size` byte offset.
///   3. Streams the remainder over a side-channel POST to
///      `/_comprador/sessions/<id>/append`, which the bridge appends to its
///      partial buffer and commits to MTP once total bytes match expected.
///
/// End-user experience: drag → Finder progress bar → file appears on phone.
/// No -36 dialog. The polling itself keeps the bridge's `companionRegistered`
/// gate open, which is what allows the bridge to return 200 OK on truncation
/// in the first place — without an active companion the bridge falls back to
/// surfacing -36 honestly so the user knows the upload didn't land.
///
/// See `docs/RESUMABLE-UPLOADS.md` for the architecture.
///
/// Concurrency: not MainActor-isolated. The polling Task runs off the main
/// thread; per-session work spawns child Tasks. Shared state (`inFlight`,
/// `done`) is mutated only from `runPollLoop` and the Task it spawns —
/// both of which we serialise via the actor-isolation of the calling site
/// (this class is held by AppDelegate, accessed from main-thread paths).
/// NSMetadataQuery is dispatched explicitly to `.main` for its delegate
/// callbacks because it requires a runloop.
final class ResumeCompanion {
    private let baseURL: URL
    private var task: Task<Void, Never>?
    private var inFlight: Set<String> = []   // session IDs we've kicked off
    private var done: Set<String> = []       // session IDs we've completed (or given up on)

    private let pollInterval: TimeInterval = 5.0

    /// Dedicated URLSession for the upload POST. The default
    /// `URLSession.shared` uses a 60-second `timeoutIntervalForRequest`,
    /// which is the time the connection can sit idle waiting for data.
    /// Our `POST /append` returns nothing until the bridge has finished
    /// committing the assembled file to MTP — a multi-minute SendFile
    /// over USB. With the default timeout, URLSession reports
    /// `NSURLErrorTimedOut` long before the bridge replies, the
    /// companion thinks the upload failed, and the next poll launches
    /// a duplicate (which the bridge correctly rejects with offset
    /// mismatch — no corruption, just wasted work and confusing logs).
    /// 30 minutes covers a 9 GiB MTP send at observed throughput
    /// (~22 MiB/s) with comfortable margin.
    private lazy var uploadSession: URLSession = {
        let cfg = URLSessionConfiguration.default
        cfg.timeoutIntervalForRequest = 1800   // 30 min idle
        cfg.timeoutIntervalForResource = 7200  // 2 h total cap
        return URLSession(configuration: cfg)
    }()

    init(bridgeURL: URL) {
        self.baseURL = bridgeURL
    }

    /// Stop polling and abandon any in-flight resumes. Safe to call repeatedly.
    func stop() {
        task?.cancel()
        task = nil
        inFlight.removeAll()
        done.removeAll()
    }

    /// Begin polling. Replaces any existing poll task.
    func start() {
        stop()
        cprLog("ResumeCompanion: starting poll loop against %@", baseURL.absoluteString)
        task = Task { [weak self] in
            await self?.runPollLoop()
        }
    }

    private func runPollLoop() async {
        var firstPollLogged = false
        var pollFailures = 0
        while !Task.isCancelled {
            do {
                let sessions = try await fetchSessions()
                if !firstPollLogged {
                    cprLog("ResumeCompanion: first poll succeeded, %d session(s) pending", sessions.count)
                    firstPollLogged = true
                }
                pollFailures = 0
                for session in sessions {
                    if inFlight.contains(session.id) || done.contains(session.id) {
                        continue
                    }
                    inFlight.insert(session.id)
                    Task { [weak self] in
                        await self?.handle(session: session)
                    }
                }
            } catch {
                // Bridge unreachable (mid-restart, USB renumeration, etc.).
                // Log the first failure and then every 12th (~1/min at the
                // default 5s poll interval) to surface persistent issues
                // without spamming the log on transient blips.
                pollFailures += 1
                if pollFailures == 1 || pollFailures % 12 == 0 {
                    cprLog("ResumeCompanion: poll #%d failed: %@", pollFailures, "\(error)")
                }
            }
            try? await Task.sleep(nanoseconds: UInt64(pollInterval * 1_000_000_000))
        }
        cprLog("ResumeCompanion: poll loop exited")
    }

    // MARK: - Per-session work

    private func handle(session: SessionMeta) async {
        defer { inFlight.remove(session.id) }
        cprLog("ResumeCompanion: handling stranded session %@ (%@) %lld/%lld",
              session.id, session.baseName, session.receivedSize, session.expectedSize)

        let candidates = await findSourceCandidates(name: session.baseName, expectedSize: session.expectedSize)
        switch candidates.count {
        case 0:
            cprLog("ResumeCompanion: no source candidate for %@ (size %lld) — needs chooser fallback",
                  session.baseName, session.expectedSize)
            await postNotification(
                title: "Couldn't auto-complete \(session.baseName)",
                body: "The original file isn't in Spotlight's index. Click to choose it manually.",
                sessionID: session.id
            )
            done.insert(session.id)

        case 1:
            do {
                try await complete(session: session, sourceURL: candidates[0])
                done.insert(session.id)
                cprLog("ResumeCompanion: completed %@ via %@", session.baseName, candidates[0].path)
            } catch {
                cprLog("ResumeCompanion: complete failed for %@: %@", session.baseName, "\(error)")
                // Don't mark as done — let the next poll retry.
            }

        default:
            cprLog("ResumeCompanion: %d candidates for %@ — needs chooser fallback",
                  candidates.count, session.baseName)
            await postNotification(
                title: "Multiple files match \(session.baseName)",
                body: "Click to pick which one to upload.",
                sessionID: session.id
            )
            done.insert(session.id)
        }
    }

    private func complete(session: SessionMeta, sourceURL: URL) async throws {
        // Sanity check the source still has the expected size — if it's been
        // edited or replaced since the drag started, we'd send wrong bytes.
        let attrs = try FileManager.default.attributesOfItem(atPath: sourceURL.path)
        if let actual = attrs[.size] as? Int64, actual != session.expectedSize {
            throw CompanionError.sizeMismatch(expected: session.expectedSize, actual: actual)
        }

        let remaining = session.expectedSize - session.receivedSize
        guard remaining > 0 else { return }

        let url = baseURL.appendingPathComponent("_comprador/sessions/\(session.id)/append")
        var components = URLComponents(url: url, resolvingAgainstBaseURL: false)!
        components.queryItems = [URLQueryItem(name: "offset", value: "\(session.receivedSize)")]
        var request = URLRequest(url: components.url!)
        request.httpMethod = "POST"
        request.setValue("application/octet-stream", forHTTPHeaderField: "Content-Type")
        request.setValue("\(remaining)", forHTTPHeaderField: "Content-Length")

        // Stream the remainder. URLSession.upload(for:fromFile:) doesn't
        // support offsets, and reading 6+ GiB into memory isn't an option.
        // BoundedFileInputStream wraps a seeked FileHandle with a length cap.
        let stream = try BoundedFileInputStream(
            fileURL: sourceURL,
            offset: UInt64(session.receivedSize),
            length: remaining
        )
        request.httpBodyStream = stream

        let (data, response) = try await uploadSession.data(for: request)
        guard let http = response as? HTTPURLResponse, (200..<300).contains(http.statusCode) else {
            let body = String(data: data, encoding: .utf8) ?? ""
            throw CompanionError.badResponse(
                status: (response as? HTTPURLResponse)?.statusCode ?? -1,
                body: body
            )
        }
    }

    // MARK: - Source discovery

    private func findSourceCandidates(name: String, expectedSize: Int64) async -> [URL] {
        await withCheckedContinuation { (continuation: CheckedContinuation<[URL], Never>) in
            let query = NSMetadataQuery()
            query.predicate = NSPredicate(
                format: "%K == %@ AND %K == %lld",
                NSMetadataItemFSNameKey, name,
                NSMetadataItemFSSizeKey, expectedSize
            )
            query.searchScopes = [NSMetadataQueryLocalComputerScope]

            var token: NSObjectProtocol?
            token = NotificationCenter.default.addObserver(
                forName: .NSMetadataQueryDidFinishGathering,
                object: query,
                queue: .main
            ) { _ in
                query.disableUpdates()
                var urls: [URL] = []
                for i in 0..<query.resultCount {
                    guard
                        let item = query.result(at: i) as? NSMetadataItem,
                        let path = item.value(forAttribute: NSMetadataItemPathKey) as? String
                    else { continue }
                    urls.append(URL(fileURLWithPath: path))
                }
                query.stop()
                if let t = token {
                    NotificationCenter.default.removeObserver(t)
                }
                continuation.resume(returning: urls)
            }

            DispatchQueue.main.async {
                query.start()
            }
        }
    }

    // MARK: - Notifications

    private func postNotification(title: String, body: String, sessionID: String) async {
        let center = UNUserNotificationCenter.current()
        // Best-effort authorization request; if denied, the notification just
        // doesn't appear. We never gate the resume on this.
        _ = try? await center.requestAuthorization(options: [.alert, .sound])
        let content = UNMutableNotificationContent()
        content.title = title
        content.body = body
        content.userInfo = ["sessionID": sessionID]
        let request = UNNotificationRequest(
            identifier: "comprador.resume.\(sessionID)",
            content: content,
            trigger: nil
        )
        try? await center.add(request)
    }

    // MARK: - HTTP

    private func fetchSessions() async throws -> [SessionMeta] {
        let url = baseURL.appendingPathComponent("_comprador/sessions")
        let (data, response) = try await URLSession.shared.data(from: url)
        guard let http = response as? HTTPURLResponse, http.statusCode == 200 else {
            throw CompanionError.badResponse(
                status: (response as? HTTPURLResponse)?.statusCode ?? -1,
                body: String(data: data, encoding: .utf8) ?? ""
            )
        }
        return try JSONDecoder().decode([SessionMeta].self, from: data)
    }
}

// MARK: - Wire types

private struct SessionMeta: Decodable {
    let id: String
    let path: String
    let baseName: String
    let expectedSize: Int64
    let receivedSize: Int64

    private enum CodingKeys: String, CodingKey {
        case id
        case path
        case baseName = "base_name"
        case expectedSize = "expected_size"
        case receivedSize = "received_size"
    }
}

enum CompanionError: Error, CustomStringConvertible {
    case sizeMismatch(expected: Int64, actual: Int64)
    case badResponse(status: Int, body: String)

    var description: String {
        switch self {
        case .sizeMismatch(let expected, let actual):
            return "source file size \(actual) doesn't match expected \(expected)"
        case .badResponse(let status, let body):
            return "HTTP \(status): \(body)"
        }
    }
}

// MARK: - Streaming source reader

/// InputStream that reads a slice of a file starting at a given offset.
///
/// URLSession's upload-from-file shortcut doesn't take an offset, so for
/// resumable uploads we need our own stream that seeks before reading and
/// caps total bytes delivered. Pure Foundation; no 3rd-party dependencies.
final class BoundedFileInputStream: InputStream {
    private let handle: FileHandle
    private var remaining: Int64
    private var status: Stream.Status = .notOpen
    private var capturedError: NSError?
    // Required by InputStream; we don't actually use these.
    private var _delegate: StreamDelegate?

    init(fileURL: URL, offset: UInt64, length: Int64) throws {
        self.handle = try FileHandle(forReadingFrom: fileURL)
        try self.handle.seek(toOffset: offset)
        self.remaining = length
        // InputStream's designated initializers all want some seed data;
        // pass an empty Data to satisfy the contract. We override read()
        // entirely so this never gets touched.
        super.init(data: Data())
    }

    override func open() {
        status = .open
    }

    override func close() {
        if status == .closed { return }
        status = .closed
        try? handle.close()
    }

    override var streamStatus: Stream.Status { status }
    override var streamError: Error? { capturedError }

    override var hasBytesAvailable: Bool {
        return status == .open && remaining > 0
    }

    override func read(_ buffer: UnsafeMutablePointer<UInt8>, maxLength len: Int) -> Int {
        guard status == .open else { return -1 }
        if remaining <= 0 { return 0 }
        let want = min(Int(remaining), len)
        guard let data = try? handle.read(upToCount: want) else {
            capturedError = NSError(
                domain: "Comprador.ResumeCompanion",
                code: 1,
                userInfo: [NSLocalizedDescriptionKey: "FileHandle.read failed"]
            )
            status = .error
            return -1
        }
        if data.isEmpty {
            // Premature EOF — the file shrank under us. Caller should treat
            // as failure; we report 0 to terminate the read cleanly.
            return 0
        }
        data.copyBytes(to: buffer, count: data.count)
        remaining -= Int64(data.count)
        return data.count
    }

    override func getBuffer(_ buffer: UnsafeMutablePointer<UnsafeMutablePointer<UInt8>?>, length: UnsafeMutablePointer<Int>) -> Bool {
        return false
    }

    // Quiet the Foundation runtime — InputStream has a few methods that
    // expect a runloop scheduling story, which we don't use (URLSession
    // handles scheduling internally for httpBodyStream).
    override func schedule(in aRunLoop: RunLoop, forMode mode: RunLoop.Mode) {}
    override func remove(from aRunLoop: RunLoop, forMode mode: RunLoop.Mode) {}
    override func property(forKey key: Stream.PropertyKey) -> Any? { nil }
    override func setProperty(_ property: Any?, forKey key: Stream.PropertyKey) -> Bool { false }
    override var delegate: StreamDelegate? {
        get { _delegate }
        set { _delegate = newValue }
    }
}
