// sckcap: capture the main display with ScreenCaptureKit, encode H.264 with VideoToolbox,
// write Annex-B NAL units to stdout. Usage: sckcap [fps] [width] [bitrate_bps]
import Foundation
import ScreenCaptureKit
import AVFoundation
import VideoToolbox
import CoreMedia

let args = CommandLine.arguments
let fps = Int(args.count > 1 ? args[1] : "60") ?? 60
let outWidth = Int(args.count > 2 ? args[2] : "1280") ?? 1280
let bitrate = Int(args.count > 3 ? args[3] : "6000000") ?? 6_000_000

func log(_ s: String) { FileHandle.standardError.write((s + "\n").data(using: .utf8)!) }

final class Encoder {
    var session: VTCompressionSession?
    let out = FileHandle.standardOutput
    var frames = 0
    var lastReport = Date()

    func setup(width: Int, height: Int) {
        var s: VTCompressionSession?
        let st = VTCompressionSessionCreate(allocator: nil, width: Int32(width), height: Int32(height), codecType: kCMVideoCodecType_H264,
            encoderSpecification: nil, imageBufferAttributes: nil, compressedDataAllocator: nil,
            outputCallback: nil, refcon: nil, compressionSessionOut: &s)
        guard st == noErr, let sess = s else { log("VTCompressionSessionCreate failed \(st)"); exit(1) }
        VTSessionSetProperty(sess, key: kVTCompressionPropertyKey_RealTime, value: kCFBooleanTrue)
        VTSessionSetProperty(sess, key: kVTCompressionPropertyKey_ProfileLevel, value: kVTProfileLevel_H264_Baseline_AutoLevel)
        VTSessionSetProperty(sess, key: kVTCompressionPropertyKey_AllowFrameReordering, value: kCFBooleanFalse)
        VTSessionSetProperty(sess, key: kVTCompressionPropertyKey_AverageBitRate, value: bitrate as CFNumber)
        VTSessionSetProperty(sess, key: kVTCompressionPropertyKey_DataRateLimits, value: [bitrate / 8, 1] as CFArray)
        VTSessionSetProperty(sess, key: kVTCompressionPropertyKey_MaxKeyFrameInterval, value: fps as CFNumber)
        VTSessionSetProperty(sess, key: kVTCompressionPropertyKey_MaxKeyFrameIntervalDuration, value: 1 as CFNumber)
        VTSessionSetProperty(sess, key: kVTCompressionPropertyKey_ExpectedFrameRate, value: fps as CFNumber)
        if #available(macOS 12.0, *) {
            VTSessionSetProperty(sess, key: kVTCompressionPropertyKey_PrioritizeEncodingSpeedOverQuality, value: kCFBooleanTrue)
        }
        VTCompressionSessionPrepareToEncodeFrames(sess)
        session = sess
        log("encoder \(width)x\(height) @\(fps) \(bitrate/1000) kbps")
    }

    func encode(_ pixelBuffer: CVPixelBuffer, pts: CMTime) {
        guard let sess = session else { return }
        VTCompressionSessionEncodeFrame(sess, imageBuffer: pixelBuffer, presentationTimeStamp: pts, duration: .invalid, frameProperties: nil, infoFlagsOut: nil) { [weak self] status, _, sample in
            guard let self = self, status == noErr, let sample = sample else { return }
            self.write(sample)
        }
    }

    func write(_ sample: CMSampleBuffer) {
        var data = Data()
        let start: [UInt8] = [0, 0, 0, 1]
        // parameter sets on keyframes
        let att = CMSampleBufferGetSampleAttachmentsArray(sample, createIfNecessary: false) as? [[CFString: Any]]
        let notSync = att?.first?[kCMSampleAttachmentKey_NotSync] as? Bool ?? false
        if !notSync {
            if let fmt = CMSampleBufferGetFormatDescription(sample) {
                var count = 0
                CMVideoFormatDescriptionGetH264ParameterSetAtIndex(fmt, parameterSetIndex: 0, parameterSetPointerOut: nil, parameterSetSizeOut: nil, parameterSetCountOut: &count, nalUnitHeaderLengthOut: nil)
                for i in 0..<count {
                    var ptr: UnsafePointer<UInt8>? = nil; var size = 0
                    CMVideoFormatDescriptionGetH264ParameterSetAtIndex(fmt, parameterSetIndex: i, parameterSetPointerOut: &ptr, parameterSetSizeOut: &size, parameterSetCountOut: nil, nalUnitHeaderLengthOut: nil)
                    if let p = ptr { data.append(contentsOf: start); data.append(p, count: size) }
                }
            }
        }
        guard let block = CMSampleBufferGetDataBuffer(sample) else { return }
        var length = 0; var ptr: UnsafeMutablePointer<Int8>? = nil
        CMBlockBufferGetDataPointer(block, atOffset: 0, lengthAtOffsetOut: nil, totalLengthOut: &length, dataPointerOut: &ptr)
        guard let base = ptr else { return }
        var off = 0
        while off + 4 <= length {
            let n = Int(UInt32(bigEndian: UnsafeRawPointer(base + off).loadUnaligned(as: UInt32.self)))
            off += 4
            if off + n > length { break }
            data.append(contentsOf: start)
            data.append(UnsafeRawPointer(base + off).assumingMemoryBound(to: UInt8.self), count: n)
            off += n
        }
        out.write(data)
        frames += 1
        if Date().timeIntervalSince(lastReport) >= 30 {
            log(String(format: "sckcap: %.1f fps", Double(frames) / Date().timeIntervalSince(lastReport)))
            frames = 0; lastReport = Date()
        }
    }
}

// Game sound: system audio from ScreenCaptureKit, encoded to Opus (20 ms packets) with the macOS encoder and written
// to file descriptor 3 as [2-byte big-endian length][packet]. Only when the server asks for it (WT_AUDIO=1).
final class AudioEncoder {
    let out = FileHandle(fileDescriptor: 3, closeOnDealloc: false)
    var conv: AVAudioConverter?
    var outFmt: AVAudioFormat?
    var dead = false
    func encode(_ sb: CMSampleBuffer) {
        guard !dead, let desc = CMSampleBufferGetFormatDescription(sb) else { return }
        let inFmt = AVAudioFormat(cmAudioFormatDescription: desc)
        if conv == nil {
            var d = AudioStreamBasicDescription(mSampleRate: 48000, mFormatID: kAudioFormatOpus, mFormatFlags: 0, mBytesPerPacket: 0,
                                                mFramesPerPacket: 960, mBytesPerFrame: 0, mChannelsPerFrame: 2, mBitsPerChannel: 0, mReserved: 0)
            guard let of = AVAudioFormat(streamDescription: &d), let c = AVAudioConverter(from: inFmt, to: of) else { log("audio: no Opus encoder"); dead = true; return }
            c.bitRate = 96000
            conv = c; outFmt = of
            log("audio: \(inFmt.sampleRate) Hz x\(inFmt.channelCount) -> Opus 96 kbps")
        }
        let frames = AVAudioFrameCount(CMSampleBufferGetNumSamples(sb))
        guard frames > 0, let pcm = AVAudioPCMBuffer(pcmFormat: inFmt, frameCapacity: frames) else { return }
        pcm.frameLength = frames
        guard CMSampleBufferCopyPCMDataIntoAudioBufferList(sb, at: 0, frameCount: Int32(frames), into: pcm.mutableAudioBufferList) == noErr else { return }
        var fed = false
        while let conv = conv, let outFmt = outFmt {
            let buf = AVAudioCompressedBuffer(format: outFmt, packetCapacity: 16, maximumPacketSize: max(conv.maximumOutputPacketSize, 1500))
            var err: NSError?
            _ = conv.convert(to: buf, error: &err) { _, status in
                if fed { status.pointee = .noDataNow; return nil }
                fed = true; status.pointee = .haveData; return pcm
            }
            if buf.packetCount == 0 { break }
            guard let descs = buf.packetDescriptions else { break }
            var data = Data()
            for i in 0..<Int(buf.packetCount) {
                let pd = descs[i]
                let n = Int(pd.mDataByteSize)
                data.append(UInt8(n >> 8)); data.append(UInt8(n & 0xff))
                data.append(Data(bytes: buf.data.advanced(by: Int(pd.mStartOffset)), count: n))
            }
            do { try out.write(contentsOf: data) } catch { log("audio: output closed"); dead = true; return }
        }
    }
}
let audioOn = ProcessInfo.processInfo.environment["WT_AUDIO"] == "1"

final class Output: NSObject, SCStreamOutput, SCStreamDelegate {
    let audio = AudioEncoder()
    let enc = Encoder()
    var ready = false
    func stream(_ stream: SCStream, didOutputSampleBuffer sb: CMSampleBuffer, of type: SCStreamOutputType) {
        if type == .audio { if sb.isValid { audio.encode(sb) }; return }
        guard type == .screen, sb.isValid, let pb = CMSampleBufferGetImageBuffer(sb) else { return }
        if let att = CMSampleBufferGetSampleAttachmentsArray(sb, createIfNecessary: false) as? [[SCStreamFrameInfo: Any]],
           let s = att.first?[.status] as? Int, s != SCFrameStatus.complete.rawValue { return }
        if !ready { enc.setup(width: CVPixelBufferGetWidth(pb), height: CVPixelBufferGetHeight(pb)); ready = true }
        enc.encode(pb, pts: CMSampleBufferGetPresentationTimeStamp(sb))
    }
    func stream(_ stream: SCStream, didStopWithError error: Error) { log("stream stopped: \(error)"); exit(2) }
}

let output = Output()
var stream: SCStream?
Task {
    do {
        let content = try await SCShareableContent.excludingDesktopWindows(false, onScreenWindowsOnly: true)
        guard let display = content.displays.first(where: { CGDisplayIsMain($0.displayID) != 0 }) ?? content.displays.first else { log("no display"); exit(1) }
        let filter = SCContentFilter(display: display, excludingWindows: [])
        let cfg = SCStreamConfiguration()
        let scale = Double(outWidth) / Double(display.width)
        cfg.width = outWidth
        cfg.height = Int((Double(display.height) * scale / 2).rounded()) * 2
        cfg.minimumFrameInterval = CMTime(value: 1, timescale: CMTimeScale(fps))
        cfg.queueDepth = 4
        cfg.pixelFormat = kCVPixelFormatType_420YpCbCr8BiPlanarVideoRange
        cfg.showsCursor = false
        cfg.colorSpaceName = CGColorSpace.sRGB
        if audioOn { cfg.capturesAudio = true; cfg.sampleRate = 48000; cfg.channelCount = 2; cfg.excludesCurrentProcessAudio = true }
        let s = SCStream(filter: filter, configuration: cfg, delegate: output)
        try s.addStreamOutput(output, type: .screen, sampleHandlerQueue: DispatchQueue(label: "cap", qos: .userInteractive))
        if audioOn { try s.addStreamOutput(output, type: .audio, sampleHandlerQueue: DispatchQueue(label: "audio", qos: .userInteractive)) }
        try await s.startCapture()
        stream = s
        log("sckcap: capturing display \(display.width)x\(display.height) -> \(cfg.width)x\(cfg.height) @\(fps)")
    } catch { log("sckcap error: \(error)"); exit(1) }
}
signal(SIGPIPE) { _ in exit(0) }
signal(SIGTERM) { _ in exit(0) }
RunLoop.main.run()
