import Foundation
import ScreenCaptureKit
import CoreMedia

// Counts frames delivered by ScreenCaptureKit for the main display over N seconds.
final class Counter: NSObject, SCStreamOutput {
    var n = 0
    func stream(_ stream: SCStream, didOutputSampleBuffer sb: CMSampleBuffer, of type: SCStreamOutputType) {
        if type == .screen, sb.isValid, CMSampleBufferGetNumSamples(sb) > 0 {
            // only count frames that carry a real image (status complete)
            if let att = CMSampleBufferGetSampleAttachmentsArray(sb, createIfNecessary: false) as? [[SCStreamFrameInfo: Any]],
               let s = att.first?[.status] as? Int, s == SCFrameStatus.complete.rawValue { n += 1 }
        }
    }
}
let seconds = Double(CommandLine.arguments.dropFirst().first ?? "5") ?? 5
let sem = DispatchSemaphore(value: 0)
var result = ""
Task {
    do {
        let content = try await SCShareableContent.excludingDesktopWindows(false, onScreenWindowsOnly: true)
        guard let display = content.displays.first(where: { CGDisplayIsMain($0.displayID) != 0 }) ?? content.displays.first else { result = "no display"; sem.signal(); return }
        let filter = SCContentFilter(display: display, excludingWindows: [])
        let cfg = SCStreamConfiguration()
        cfg.width = display.width; cfg.height = display.height
        cfg.minimumFrameInterval = CMTime(value: 1, timescale: 120)
        cfg.queueDepth = 5
        cfg.pixelFormat = kCVPixelFormatType_420YpCbCr8BiPlanarVideoRange
        let out = Counter()
        let stream = SCStream(filter: filter, configuration: cfg, delegate: nil)
        try stream.addStreamOutput(out, type: .screen, sampleHandlerQueue: DispatchQueue(label: "cap"))
        try await stream.startCapture()
        try await Task.sleep(nanoseconds: UInt64(seconds * 1e9))
        try await stream.stopCapture()
        result = String(format: "sck: %.1f fps over %.0fs (%d frames, display %dx%d)", Double(out.n)/seconds, seconds, out.n, display.width, display.height)
    } catch { result = "error: \(error)" }
    sem.signal()
}
sem.wait()
print(result)
