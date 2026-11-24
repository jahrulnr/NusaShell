using System.Collections.Generic;

namespace Acme.Core
{
    // Worker processes jobs.
    public class Worker
    {
        public int Run(Job job)
        {
            return job.Size;
        }
    }

    // Job is a unit of work.
    public record Job(int Size, string Name);

    // Point is an XY pair.
    public struct Point
    {
        public int X;
        public int Y;
    }
}

namespace Acme.Util
{
    public class Clock
    {
        private int Reset() { return 0; }
    }
}
