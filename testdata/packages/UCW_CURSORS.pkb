create or replace package body ucw_cursors as

  g_calls pls_integer := 0;

  cursor c_levels (p_max in pls_integer) is
    select level as lvl, 'item ' || level as label
      from dual
   connect by level <= p_max;

  procedure bump
  is
  begin
    g_calls := g_calls + 1;
  end bump;

  function numbers_csv (p_count in pls_integer) return varchar2
  is
    type t_label_tab is table of varchar2(30);
    l_labels t_label_tab;
    l_csv    varchar2(4000);
  begin
    bump;
    for r in c_levels(p_count) loop
      l_csv := l_csv || case when l_csv is null then null else ',' end || r.lvl;
    end loop;

    select label bulk collect into l_labels
      from table(sys.odcivarchar2list('x', 'y', 'z'))
         , (select 'item ' || column_value as label from table(sys.odcivarchar2list('x', 'y', 'z')))
     where rownum <= 3
       and 1 = 1;
    return l_csv;
  end numbers_csv;

  procedure init
  is
  begin
    g_calls := 0;
  end init;

  function call_count return pls_integer
  is
  begin
    return g_calls;
  end call_count;

begin
  g_calls := 0;
end ucw_cursors;
/
