import Foundation

// Frame is a render frame.
struct Frame {
    var index: Int
}

// Drawable can draw itself.
protocol Drawable {
    func draw()
}

// View is a screen node.
class View {
    func draw() {}
}

extension View {
    func measure() -> Int { return 0 }
}

func render(_ v: View) -> Frame {
    return Frame(index: 0)
}

private func hidden() {}
