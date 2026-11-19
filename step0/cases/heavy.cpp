// EXPECTED: AC - realistic C++ submission; its compile is what the box must
// survive twice over. Uses real STL weight so cc1plus actually costs memory.
#include <algorithm>
#include <iostream>
#include <map>
#include <sstream>
#include <string>
#include <vector>

int main()
{
    long long a, b;
    if (!(std::cin >> a >> b))
        return 1;

    std::vector<long long> v(1000);
    for (std::size_t i = 0; i < v.size(); ++i)
        v[i] = a + b;

    std::map<std::string, std::vector<long long>> m;
    m["sum"] = v;
    std::sort(v.begin(), v.end());

    std::ostringstream os;
    os << v.back();
    std::cout << os.str() << "\n";
    return 0;
}
