create or replace package body ucw_collections as

  type t_pair is record (
    l_key   varchar2(30),
    l_value number
  );
  type t_pair_tab is table of t_pair index by pls_integer;

  gc_max_rows constant pls_integer := 100;
  g_cache     t_pair_tab;

  function sum_range (p_from in pls_integer, p_to in pls_integer) return number
  is
    l_numbers t_number_tab;
    l_total   number := 0;
  begin
    select level bulk collect into l_numbers
      from dual
   connect by level <= gc_max_rows;

    for i in p_from .. p_to loop
      if l_numbers.exists(i) then
        l_total := l_total + l_numbers(i);
      end if;
    end loop;

    for i in 1..l_numbers.count loop
      null;
    end loop;
    return l_total;
  end sum_range;

  function top_squares (p_count in pls_integer) return varchar2
  is
    l_pairs  t_pair_tab;
    l_result varchar2(4000);
    l_idx    pls_integer;
  begin
    for i in 1 .. least(p_count, gc_max_rows) loop
      l_pairs(i).l_key   := 'sq' || i;
      l_pairs(i).l_value := i * i;
    end loop;
    g_cache := l_pairs;

    l_idx := g_cache.first;
    while l_idx is not null loop
      l_result := l_result || g_cache(l_idx).l_key || '=' || g_cache(l_idx).l_value || ';';
      l_idx    := g_cache.next(l_idx);
    end loop;
    return l_result;
  end top_squares;

end ucw_collections;
/
